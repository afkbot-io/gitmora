#!/usr/bin/env python3
"""Synthetic Entire/Git AI objects through real Git/CLI/SQLite/restarted API/index/quote.

No upstream producer, hosted provider, production content or model is used.
"""
import importlib.util
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('compatibility_api', ROOT / 'scripts/smoke-api.py')
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def main():
    schema = 'compatibility_' + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    env = {k: v for k, v in os.environ.items() if k in {'PATH', 'HOME', 'TMPDIR', 'LANG', 'USER', 'SHELL'}}
    env.update(WORKGRAPH_DATABASE_URL=api.BASE_DSN + '&search_path=' + schema,
               GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM='1',
               GIT_AUTHOR_NAME='Import fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid',
               GIT_COMMITTER_NAME='Import fixture', GIT_COMMITTER_EMAIL='fixture@example.invalid')
    process = interrupted = None
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        assert api.command(['migrate'], env).returncode == 0
        seeded = api.command(['bootstrap', '--project', 'Imports', '--repository', 'Synthetic source',
                              '--gitlab-url', 'https://gitlab.internal', '--gitlab-project-id', '42'], env)
        assert seeded.returncode == 0
        identity = json.loads(seeded.stdout)
        env['WORKGRAPH_TOKEN'] = identity['token']
        process, address = api.start_api(env)

        def request(path, body=None):
            req = urllib.request.Request('http://' + address + path,
                data=json.dumps(body).encode() if body is not None else None,
                headers={'Authorization': 'Bearer ' + identity['token'], 'Content-Type': 'application/json'})
            with opener.open(req, timeout=15) as response:
                return json.load(response)

        with tempfile.TemporaryDirectory(prefix='gitmora-import-') as directory:
            root = Path(directory)

            def run(args, stdin=None):
                result = subprocess.run(args, cwd=root, env=env, input=stdin, capture_output=True, text=True, timeout=90)
                assert result.returncode == 0, 'Synthetic import subprocess failed'
                return result.stdout.strip()

            def git(*args, stdin=None):
                return run(['git', *args], stdin)

            def cli(*args):
                return json.loads(run([str(ROOT / 'bin/workgraph'), *args]))

            git('init', '-q', '-b', 'main')
            git('remote', 'add', 'origin', str(root / 'unused.git'))
            (root / 'code.go').write_text('one\ntwo\nthree\n')
            git('add', 'code.go'); git('commit', '-qm', 'source target')
            target = git('rev-parse', 'HEAD')
            cli('init', '--server', 'http://' + address, '--repository-id', identity['repository_id'])
            note = 'code.go\n  abcdef0 1-3\n---\n' + json.dumps({'schema_version': 'authorship/3.0.0', 'base_commit_sha': target,
                'prompts': {'abcdef0': {'agent_id': {'tool': 'fixture', 'id': 'original-session', 'model': 'local'}}}})
            git('notes', '--ref=ai', 'add', '-m', note, target)
            note_ref = git('rev-parse', 'refs/notes/ai')
            ai = cli('import', 'git-ai', '--commit', target, '--sync')
            assert ai['windows'] == 1 and ai['delivery']['attempted'] == 0

            def blob(raw):
                return git('hash-object', '-w', '--stdin', stdin=raw)

            def tree(entries):
                return git('mktree', '-z', stdin=''.join(f'{mode} {kind} {oid}\t{name}\0' for mode, kind, oid, name in entries))

            checkpoint = '01M63VCB10XSZD4BQ2PQMWGPXV'
            metadata = json.dumps({'checkpoint_id': checkpoint, 'session_id': 'original-entire-session',
                'created_at': '2026-10-29T12:50:26Z', 'agent': 'fixture', 'files_touched': ['code.go']})
            text = 'CompatibilityRecoveryMarker ' + 'x' * 50000
            transcript = '\n'.join(json.dumps({'v': 1, 'type': 'user', 'content': text + str(i)}) for i in range(120)) + '\n'
            session_tree = tree([('100644', 'blob', blob(metadata), 'metadata.json'),
                                 ('100644', 'blob', blob(transcript), 'transcript.jsonl')])
            summary = json.dumps({'checkpoint_id': checkpoint, 'commit_sha': target,
                'sessions': [{'metadata': '/0/metadata.json', 'compact_transcript': '/0/transcript.jsonl'}]})
            checkpoint_tree = tree([('100644', 'blob', blob(summary), 'metadata.json'), ('040000', 'tree', session_tree, '0')])
            entire_ref = 'refs/entire/checkpoints/XV/' + checkpoint
            entire_object = git('commit-tree', checkpoint_tree, stdin='Synthetic checkpoint\n')
            git('update-ref', entire_ref, entire_object)
            args = [str(ROOT / 'bin/workgraph'), 'import', 'entire', '--checkpoint', checkpoint, '--commit', target]
            interrupted = subprocess.Popen(args, cwd=root, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            deadline, partial = time.monotonic() + 60, 0
            with sqlite3.connect(root / '.git/workgraph/state.sqlite', timeout=.1) as connection:
                while time.monotonic() < deadline and interrupted.poll() is None:
                    partial = connection.execute('SELECT count(*) FROM recordings').fetchone()[0] - 1
                    if partial:
                        interrupted.send_signal(signal.SIGSTOP); interrupted.kill(); break
                    time.sleep(.001)
            interrupted.communicate(timeout=10)
            assert partial > 0 and interrupted.returncode < 0, 'Import did not stop after a durable prefix'
            interrupted = None
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == '0', 'Import granted publication'
            git('update-ref', entire_ref, target)
            replay = ['import', 'entire', '--checkpoint', checkpoint, '--commit', target,
                      '--source-ref', entire_ref, '--source-object', entire_object]
            entire = cli(*replay)
            assert entire['windows'] > partial and len(set(entire['session_ids'])) == entire['windows']
            (root / 'code.go').write_text('human change\n')
            git('add', 'code.go'); git('commit', '-qm', 'later human work')
            human = git('rev-parse', 'HEAD')
            process.terminate(); process.wait(timeout=10); process.stderr.close(); process = None
            # Explicit publication persists even though the real API is currently unavailable.
            offline = subprocess.run([str(ROOT / 'bin/workgraph'), 'sync', '--include-uncommitted'], cwd=root,
                env=env, capture_output=True, text=True, timeout=60)
            assert offline.returncode != 0 and cli('status')['queue']['pending'] > 0, 'Offline delivery was not durably queued'
            process, address = api.start_api(env, address)
            assert cli('sync', '--retry')['failed'] == 0
            expected_ids = ai['session_ids'] + entire['session_ids']
            messages = []
            for session_id in expected_ids:
                saved = request(f'/v1/repositories/{identity["repository_id"]}/sessions/{session_id}')
                snapshot, origin = saved['snapshot'], saved['snapshot']['origin']
                assert snapshot['commits'] == [target] and human not in snapshot['commits']
                assert origin['target_commit'] == target
                if origin['system'] == 'entire':
                    assert origin['ref_object'] == entire_object and origin['object_id'] == session_tree
                    messages.extend(snapshot['messages'])
                    if origin['part'] > 1:
                        assert saved['parent_reference_verified']
                else:
                    assert origin['ref_object'] == note_ref and origin['claims'][0]['end'] == 3
            assert [m['text'] for m in messages] == [text + str(i) for i in range(120)], 'Full source text/order changed'
            revisions = api.sql(f'SELECT count(*) FROM "{schema}".capture_revisions')
            for _ in range(len(expected_ids) + 2):
                assert api.command(['worker', '--once'], env).returncode == 0
            found = request('/v1/context/search', {'project_id': identity['project_id'], 'text': 'CompatibilityRecoveryMarker', 'limit': 5})
            assert found['items'], 'Imported evidence not indexed'
            quote = request('/v1/context/expand', {'evidence_id': found['items'][0]['id'], 'limit': 100})
            assert quote['content'] and quote['content_digest'] == found['items'][0]['content_digest']
            assert cli(*replay)['session_ids'] == entire['session_ids']
            cli('sync')
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_revisions') == revisions
            print(json.dumps({'result': 'passed', 'source': 'synthetic Entire/Git AI objects through actual Git',
                'entire_windows': entire['windows'], 'killed_after_windows': partial, 'source_ref_moved': True,
                'publication': 'explicit', 'api_restart': True, 'exact_quote': True, 'replay': 'no_new_revisions'}))
    finally:
        for child in (interrupted, process):
            if child:
                if child.poll() is None:
                    child.kill()
                child.wait(timeout=10)
                for stream in (child.stdin, child.stdout, child.stderr):
                    if stream and not stream.closed:
                        stream.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == '__main__':
    main()
