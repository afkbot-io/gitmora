"""Exercise the shipped native API command and stdio MCP bridge against a real isolated API."""
import json
import os
import select
import subprocess


def verify(root, url, token, query, evidence, revoke):
    environment = {k: v for k, v in os.environ.items() if k in {'PATH', 'HOME', 'TMPDIR', 'USER', 'LANG', 'SHELL'}}
    environment['WORKGRAPH_TOKEN'] = token
    command = [str(root / 'bin/gitmora'), 'api', 'post', '/v1/context/search', '--server', url, '--input', '-']
    result = subprocess.run(command, input=json.dumps(query), env=environment, cwd=root, text=True, capture_output=True, timeout=30)
    assert result.returncode == 0 and token not in result.stdout + result.stderr
    response = json.loads(result.stdout)
    assert response['status'] == 200 and evidence in {item['id'] for item in response['body']['items']}
    process = subprocess.Popen([str(root / 'bin/gitmora'), 'mcp', '--server', url], env=environment, cwd=root,
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
    sequence = 0

    def rpc(method, params):
        nonlocal sequence
        sequence += 1
        process.stdin.write(json.dumps(dict(jsonrpc='2.0', id=sequence, method=method, params=params)) + '\n'); process.stdin.flush()
        while True:
            ready, _, _ = select.select([process.stdout], [], [], 20)
            assert ready, 'Native MCP did not return a protocol response'
            line = process.stdout.readline()
            assert line and token not in line, 'Native MCP closed or exposed authority'
            reply = json.loads(line)
            if reply.get('id') == sequence:
                return reply

    try:
        initialized = rpc('initialize', dict(protocolVersion='2025-11-25', capabilities={}, clientInfo=dict(name='native-context-verification', version='1')))
        assert 'result' in initialized
        process.stdin.write(json.dumps(dict(jsonrpc='2.0', method='notifications/initialized')) + '\n'); process.stdin.flush()
        tools = rpc('tools/list', {})
        assert {tool['name'] for tool in tools['result']['tools']} == {'context.search', 'context.browse', 'context.pack', 'context.expand', 'context.resolve', 'context.file'}
        assert all(tool['annotations']['readOnlyHint'] for tool in tools['result']['tools'])
        searched = rpc('tools/call', dict(name='context.search', arguments=query))
        assert evidence in {item['id'] for item in searched['result']['structuredContent']['result']['items']}
        page = searched['result']['structuredContent']['result']
        if page.get('next'):
            continued = rpc('tools/call', dict(name='context.search', arguments={**query, 'after': page['next']}))
            assert not continued['result'].get('isError'), 'Native MCP continuation failed'
            later = continued['result']['structuredContent']['result']
            assert not {item['id'] for item in page['items']}.intersection(item['id'] for item in later['items']), 'Native MCP repeated a search result'
        expanded = rpc('tools/call', dict(name='context.expand', arguments=dict(evidence_id=evidence, limit=40)))
        assert expanded['result']['structuredContent']['result']['id'] == evidence
        revoke()
        rejected = rpc('tools/call', dict(name='context.search', arguments=query))
        assert 'error' in rejected or rejected.get('result', {}).get('isError'), 'Revoked native authority reused'
        result = subprocess.run(command, input=json.dumps(query), env=environment, cwd=root, text=True, capture_output=True, timeout=30)
        assert result.returncode != 0 and not result.stdout and token not in result.stderr
    finally:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill(); process.wait(timeout=5)
        for stream in (process.stdin, process.stdout, process.stderr):
            stream.close()
