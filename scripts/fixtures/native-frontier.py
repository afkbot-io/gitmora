"""Kill shipped native coordinator processes after durable phases; all inputs are synthetic."""
import json
import selectors
import sqlite3
import subprocess


def exercise(root, environment, repository, command, request, origin, query):
    source = request(origin, "/session?" + query, {"title": "Synthetic crash frontier"})
    session = source["id"]
    request(origin, f"/session/{session}/message?" + query,
            {"noReply": True, "parts": [{"type": "text", "text": "Durable tool boundary source"}]})
    env = dict(environment, FRONTIER_SESSION=session)
    program = """
import {createOpencodeClient} from '@opencode-ai/sdk';
const {Coordinator}=await import(process.env.PAGE_COORDINATOR);
const {LocalCollector}=await import(process.env.PAGE_COLLECTOR);
const sdk=createOpencodeClient({baseUrl:process.env.PAGE_ORIGIN});
const local=new LocalCollector(process.env.PAGE_CLI);
const held=()=>new Promise(()=>{process.stdout.write('durable-phase\\n');setInterval(()=>{},1000);});
const source={session:{get:q=>sdk.session.get(q),messages:process.env.FRONTIER_PHASE==='bound'?q=>sdk.session.messages(q):held}};
if(process.env.FRONTIER_PHASE==='bound')local.apply=held;
const coordinator=new Coordinator(source,local,process.env.PAGE_DIRECTORY,()=>{throw new Error('Unexpected native failure');});
if(process.env.FRONTIER_PHASE==='begin')await coordinator.before(process.env.FRONTIER_SESSION,'crash-call');
else await coordinator.after(process.env.FRONTIER_SESSION,process.env.FRONTIER_PHASE==='bound'?'bound-call':'crash-call');
throw new Error('Expected suspended durable phase');
"""

    def kill_at(phase):
        child = subprocess.Popen(["node", "--input-type=module", "-e", program], cwd=root,
                                 env=dict(env, FRONTIER_PHASE=phase), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(child.stdout, selectors.EVENT_READ)
                assert selector.select(20), "Native durable phase did not become observable"
                assert child.stdout.readline().strip() == "durable-phase", "Native process failed before its durable phase"
            child.kill(); child.wait(timeout=5)
            assert child.returncode < 0, "Native coordinator was not killed"
        finally:
            if child.poll() is None:
                child.kill(); child.wait(timeout=5)
            child.stdout.close(); child.stderr.close()

    cli = str(root / "bin/workgraph")
    tool = {"agent": "opencode", "source_session_id": "opencode:" + session, "call_id": "crash-call"}
    kill_at("begin")
    pending = json.loads(command([cli, "frontier", "list"]))["frontiers"]
    first = next(row for row in pending if row["source"]["source_session_id"] == "opencode:" + session)
    assert first["after"] is None and first["target_session_id"] == "", "Unobserved end was fabricated"

    def commit(name):
        (repository / name).write_text(name)
        command(["git", "add", name]); command(["git", "commit", "-m", name])
        return command(["git", "rev-parse", "HEAD"]).strip()

    a = commit("tool-first.txt")
    b = commit("tool-second.txt")
    kill_at("end")
    human = commit("later-human.txt")
    ended = json.loads(command([cli, "frontier", "end"], input=json.dumps(tool)))
    assert ended["after"] == b, "End retry captured later human HEAD"
    resume = """
import {createOpencodeClient} from '@opencode-ai/sdk';
const {Coordinator}=await import(process.env.PAGE_COORDINATOR);
const {LocalCollector}=await import(process.env.PAGE_COLLECTOR);
const client=process.env.FRONTIER_NO_SDK?{session:{get:async()=>{throw new Error('Bound recovery read SDK');},messages:async()=>{throw new Error('Bound recovery read SDK');}}}:createOpencodeClient({baseUrl:process.env.PAGE_ORIGIN});
const coordinator=new Coordinator(client,new LocalCollector(process.env.PAGE_CLI),process.env.PAGE_DIRECTORY,()=>{throw new Error('Recovery failed');});
await coordinator.resume();
"""
    completed = subprocess.run(["node", "--input-type=module", "-e", resume], cwd=root, env=env,
                               capture_output=True, text=True, timeout=30)
    assert completed.returncode == 0, "Actual native frontier recovery failed"
    applied = json.loads(command([cli, "frontier", "get", "--id", ended["id"]]))
    assert applied["applied"] and applied["after"] == b
    # A second kill follows durable target acceptance, before association.
    second_tool = dict(tool, call_id="bound-call")
    command([cli, "frontier", "begin"], input=json.dumps(second_tool))
    d = commit("tool-bound.txt")
    kill_at("bound")
    later = commit("after-bound-human.txt")
    completed = subprocess.run(["node", "--input-type=module", "-e", resume], cwd=root,
                               env=dict(env, FRONTIER_NO_SDK="1"), capture_output=True, text=True, timeout=30)
    assert completed.returncode == 0, "Target-bound recovery required SDK or lost its boundary"
    with sqlite3.connect(repository / ".git/workgraph/state.sqlite") as ledger:
        state = json.loads(ledger.execute("SELECT state FROM recordings WHERE session_id=?",
                                        (applied["target_session_id"],)).fetchone()[0])
        assert set(state["snapshot"]["commits"]) == {a, b, d}, "Recovered interval captured later human work or lost tools"
        assert human not in state["snapshot"]["commits"] and later not in state["snapshot"]["commits"]
        private = ledger.execute("SELECT publication_allowed FROM recordings WHERE session_id=?",
                                 (applied["target_session_id"],)).fetchone()[0]
        assert private == 0, "Native crash recovery granted publication"
    return {"killed_phases": ["started", "ended", "target_bound"], "intermediate_commits": 3,
            "later_human_commits_excluded": 2, "publication": "private"}
