import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import landTool, { executeLand } from '../land.ts';
import keepGoing from '../keep-going.ts';

const adapter = fileURLToPath(new URL('../', import.meta.url));

function landFixture(t) {
  const dir = mkdtempSync(join(tmpdir(), 'land-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const cwd = join(dir, 'repo');
  mkdirSync(cwd);
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim();
  const commit = () => git('-c', 'user.name=Test', '-c', 'user.email=test@example.org', 'commit', '-qm', 'test');
  const put = (name, data) => writeFileSync(join(dir, name), data);
  const state = (value) => put('landing-state.json', JSON.stringify(value));
  git('init', '-q');
  put('repo/in', 'base'); git('add', 'in'); commit();
  const base = git('rev-parse', 'HEAD');
  put('fence.txt', 'in\nnew\n');
  const forbidden = () => { throw new Error('unexpected wake query'); };
  const land = (id = 'call', wakes = () => [], answered, transcript) => executeLand(id, 'summary', cwd, dir, base, join(dir, 'fence.txt'), wakes, answered, transcript);
  const offer = join(dir, 'landing-offer.json');
  const refuses = (attempt, reason) => {
    assert.throws(attempt, reason);
    assert.equal(existsSync(offer), false, 'a rejected call must not write an offer');
  };
  const change = () => { put('repo/in', 'change'); git('add', 'in'); commit(); return git('rev-parse', 'HEAD'); };
  return { put, state, cwd, git, base, forbidden, land, offer, refuses, change };
}

test('land rejects invalid offers', (t) => {
  const { put, state, cwd, git, forbidden, land, refuses, change } = landFixture(t);
  refuses(() => land('call', forbidden), /no commit/);
  const head = change();
  put('repo/in', 'staged'); git('add', 'in'); put('repo/new', 'untracked');
  refuses(() => land(), /in\nnew/);
  git('reset', '--hard', 'HEAD'); rmSync(join(cwd, 'new'));
  state({ refused: [{ commit: head, answer: 'gate evidence' }] });
  refuses(() => land(), /new commit is required[\s\S]*gate evidence/);
  state({ open: [{ call_id: 'other' }] });
  refuses(() => land(), /open: other/);
  assert.equal(land('other', forbidden).park, true);
  state({});
  refuses(() => land('call', () => ['task-1']), /task-1/);
  refuses(() => land('call', () => { throw new Error('journal unknown'); }), /journal unknown/);
});

test('killed before the offer exists: replay writes the offer and parks', (t) => {
  const { put, land, offer, base, change } = landFixture(t);
  const head = change();
  put('repo/outside', 'not in fence');
  assert.equal(land().park, true);
  assert.deepEqual(JSON.parse(readFileSync(offer)), { call_id: 'call', commit: head, base, summary: 'summary', transcript: '' });
});

test('session opened with no answer yet: land parks again', (t) => {
  const { land, forbidden, change } = landFixture(t);
  change();
  assert.equal(land().park, true);
  assert.equal(land('call', forbidden).park, true);
  assert.throws(() => land('other'), /open: call/);
});

test('process dies before saving the result: replay returns the same answer', (t) => {
  const { land, forbidden, offer, state, put, change } = landFixture(t);
  change();
  const answer = { call_id: 'call', kind: 'refused', stage: 'gate', text: 'run tests' };
  put('landing-answer.json', JSON.stringify(answer));
  state({ open: [{ call_id: 'call' }] });
  const expected = { content: [{ type: 'text', text: answer.text }], details: answer, isError: true };
  assert.deepEqual(land('call', forbidden), expected);
  assert.deepEqual(land('call', forbidden), expected);
  assert.equal(existsSync(offer), false);
  assert.equal(land('next', () => [], id => id === 'call', '/session/file.jsonl').park, true);
  assert.equal(JSON.parse(readFileSync(offer)).transcript, '/session/file.jsonl');
});

test('registered land passes the session file and never writes landing-transcript', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'land-execute-'));
  const old = { ...process.env };
  t.after(() => { process.env = old; rmSync(dir, { recursive: true, force: true }); });
  const cwd = join(dir, 'repo'); mkdirSync(cwd);
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim();
  const commit = () => git('-c', 'user.name=Test', '-c', 'user.email=test@example.org', 'commit', '-qm', 'test');
  git('init', '-q'); writeFileSync(join(cwd, 'in'), 'base'); git('add', 'in'); commit();
  const base = git('rev-parse', 'HEAD');
  writeFileSync(join(cwd, 'in'), 'changed'); git('add', 'in'); commit();
  writeFileSync(join(dir, 'fence.txt'), 'in\n');
  writeFileSync(join(dir, 'python3'), '#!/bin/sh\nprintf "[]\\n"\n', { mode: 0o700 });
  Object.assign(process.env, { YOLO_DURABLE_DIR: dir, TILLR_GOAL_BASE: base, BOARD_DRAIN_BRIEF: join(dir, 'brief.md'), PATH: `${dir}:${old.PATH}` });
  const handlers = {}; let tool;
  landTool({ on: (name, fn) => { handlers[name] = fn; }, registerTool: value => { tool = value; } });
  const ctx = { cwd, sessionManager: { getSessionFile: () => '/actual/session.jsonl', getBranch: () => [] } };
  tool.execute('call', { summary: 'summary' }, undefined, undefined, ctx);
  assert.equal(JSON.parse(readFileSync(join(dir, 'landing-offer.json'))).transcript, '/actual/session.jsonl');
  writeFileSync(join(dir, 'landing-answer.json'), '{}');
  if (handlers.session_start) handlers.session_start({}, ctx);
  assert.equal(existsSync(join(dir, 'landing-transcript')), false);
});

test('keep-going nudges a plain ending for land without inspecting git', (t) => {
  const old = { ...process.env };
  t.after(() => { process.env = old; });
  process.env.TILLR_GOAL_BASE = 'base';
  process.env.TILLR_CALL_SUSPENDS = '1';
  const handlers = {};
  keepGoing({ on: (name, fn) => { handlers[name] = fn; } });
  handlers.agent_end({ messages: [{ role: 'assistant', stopReason: 'stop' }] });
  return handlers.agent_before_settle({ outcome: 'completed' }).then(result => {
    assert.equal(result.continue, true);
    assert.match(result.entries[0].content, /without calling `land`/);
  });
});

test('run-pi keeps footer argv and opens an answer session without stdin', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'run-pi-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const put = (name, value, options) => writeFileSync(join(dir, name), value, options);
  const brief = '- **Target:** test (lane worktree)\n' + ['Header', 'Task', 'Fence', 'Seats', 'Settled decisions', 'Facts carried', 'Method', 'Verification commands', 'Report shape', 'Stop conditions'].map(heading => `## ${heading}\n`).join('');
  put('brief.md', brief);
  put('base_sha', 'base\n');
  put('pi', '#!/bin/sh\nprintf "%s\\n" "$@"\nprintf "STDIN:"\ncat\n', { mode: 0o700 });
  const run = () => {
    const r = spawnSync(join(adapter, 'run-pi'), ['--backend', 'pi', '--effort', 'low', '--model', 'model', '--brief', 'brief.md', '--session-id', 'id'], {
      cwd: dir, encoding: 'utf8', env: { ...process.env, PATH: `${dir}:${process.env.PATH}`, PI_CODING_AGENT_DIR: dir,
        YOLO_DURABLE_DIR: dir, TILLR_SESSION_MODE: 'resume', TILLR_PAUSE_REQUEST: '', TILLR_READ_ONLY_GROUP: '', TILLR_RESUME_PROMPT_FILE: '' },
    });
    assert.equal(r.status, 0, r.stderr || String(r.error ?? ''));
    return r.stdout;
  };
  const footer = `--print\n--mode\njson\n--model\nmodel\n--thinking\nlow\n--session\nid\n-e\n${adapter}keep-going.ts\n--no-extensions\n--no-skills\n--no-context-files\n--no-prompt-templates\nSTDIN:${brief}`;
  assert.equal(run(), footer);
  put('ending-capability', 'call-suspends\n');
  put('brief.md', brief.replace('## Report shape', '## Finishing'));
  const chosen = footer.replace('## Report shape', '## Finishing').replace('--no-extensions', `-e\n${adapter}land.ts\n--no-extensions`);
  assert.equal(run(), chosen);
  put('landing-answer.json', JSON.stringify({ call_id: 'call', text: 'answer' }));
  put('session.jsonl', [JSON.stringify({ type: 'session', id: 's' }), JSON.stringify({ type: 'message', id: 'a', parentId: null, message: { role: 'assistant', content: [{ type: 'toolCall', id: 'call' }] } })].join('\n') + '\n');
  put('landing-transcript', join(dir, 'session.jsonl'));
  assert.equal(run(), chosen.replace(/STDIN:.*/s, 'STDIN:'));
  assert.equal(readFileSync(join(dir, 'landing-delivery'), 'utf8').trim(), 'tool-result');
});

function answerLaunchFixture(t) {
  const dir = mkdtempSync(join(tmpdir(), 'run-pi-answer-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const put = (name, value, options) => writeFileSync(join(dir, name), value, options);
  put('brief.md', '- **Target:** test (lane worktree)\n' + ['Header', 'Task', 'Fence', 'Seats', 'Settled decisions', 'Facts carried', 'Method', 'Verification commands', 'Finishing', 'Stop conditions'].map(heading => `## ${heading}\n`).join(''));
  put('base_sha', 'base\n'); put('ending-capability', 'call-suspends\n');
  put('landing-answer.json', JSON.stringify({ call_id: 'call', text: 'answer text\nnext line' }));
  put('landing-transcript', join(dir, 'session.jsonl'));
  put('pi', `#!/bin/sh\nprintf '%s\\n' "$(cat landing-delivery)" >> launches\nprintf '%s\\n' "$(cat)" >> launches\nif [ "$(cat landing-delivery)" = tool-result ]; then\n  case "\${FAIL_MODE:-}" in\n    refusal) printf '%s\\n' '{"type":"error","message":"No unanswered tool call or trailing tool result to continue"}'; exit 1 ;;\n    other) echo unrelated; exit 7 ;;\n  esac\nfi\n`, { mode: 0o700 });
  const call = { type: 'message', id: 'a', parentId: null, message: { role: 'assistant', content: [{ type: 'toolCall', id: 'call' }] } };
  const result = { type: 'message', id: 'b', parentId: 'a', message: { role: 'toolResult', toolCallId: 'call' } };
  const other = { type: 'message', id: 'c', parentId: 'b', message: { role: 'toolResult', toolCallId: 'other' } };
  const compact = { type: 'compaction', id: 'c', parentId: 'a' };
  const run = (entries, mode = '') => {
    rmSync(join(dir, 'launches'), { force: true });
    if (entries !== null) put('session.jsonl', [JSON.stringify({ type: 'session', id: 's' }), ...entries.map(JSON.stringify)].join('\n') + '\n');
    return spawnSync(join(adapter, 'run-pi'), ['--backend', 'pi', '--effort', 'low', '--model', 'model', '--brief', 'brief.md', '--session-id', 'id'], {
      cwd: dir, encoding: 'utf8', env: { ...process.env, PATH: `${dir}:${process.env.PATH}`, PI_CODING_AGENT_DIR: dir, YOLO_DURABLE_DIR: dir,
        TILLR_SESSION_MODE: 'resume', TILLR_PAUSE_REQUEST: '', TILLR_READ_ONLY_GROUP: '', TILLR_RESUME_PROMPT_FILE: '', FAIL_MODE: mode },
    });
  };
  return { dir, put, call, result, other, compact, run };
}

test('transcript no longer holds the unanswered call: deliver as a message', (t) => {
  const { dir, call, result, other, compact, run } = answerLaunchFixture(t);
  for (const entries of [[call, result, other], [call, compact]]) {
    const r = run(entries); assert.equal(r.status, 0, r.stderr);
    assert.equal(readFileSync(join(dir, 'launches'), 'utf8'), 'message\nanswer text\nnext line\n');
  }
});

test('process dies after saving the result: continue from the trailing result', (t) => {
  const { dir, call, result, run } = answerLaunchFixture(t);
  let r = run([call, result]); assert.equal(r.status, 0, r.stderr);
  assert.equal(readFileSync(join(dir, 'launches'), 'utf8'), 'tool-result\n\n');
});

test('missing transcript path: run-pi opens the existing session with a message', (t) => {
  const { dir, call, run } = answerLaunchFixture(t);
  rmSync(join(dir, 'landing-transcript'));
  const r = run([call]); assert.equal(r.status, 0, r.stderr);
  assert.equal(readFileSync(join(dir, 'launches'), 'utf8'), 'message\nanswer text\nnext line\n');
});

test('recorded transcript is gone: refuse the resume before launching Pi', (t) => {
  const { dir, run } = answerLaunchFixture(t);
  const r = run(null);
  assert.equal(r.status, 1);
  assert.match(r.stderr, /Pi landing transcript not found/);
  assert.equal(existsSync(join(dir, 'launches')), false);
  assert.equal(existsSync(join(dir, 'landing-delivery')), false);
});

test('harness refuses promptless continuation: run-pi retries with a message', (t) => {
  const { dir, put, call, run } = answerLaunchFixture(t);
  let r = run([call], 'refusal'); assert.equal(r.status, 0, r.stderr);
  assert.equal(readFileSync(join(dir, 'launches'), 'utf8'), 'tool-result\n\nmessage\nanswer text\nnext line\n');
  assert.equal(readFileSync(join(dir, 'landing-delivery'), 'utf8').trim(), 'message');
  r = run([call], 'other'); assert.equal(r.status, 7, r.stderr);
  assert.equal(readFileSync(join(dir, 'launches'), 'utf8'), 'tool-result\n\n');
  assert.equal(readFileSync(join(dir, 'landing-delivery'), 'utf8').trim(), 'tool-result');
  put('landing-answer.json', '{}');
  r = run([call]); assert.equal(r.status, 2); assert.equal(existsSync(join(dir, 'launches')), false);
});

test('stop-at-gap streams park without treating it as a settle or a pause gap', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'park-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const pause = join(dir, 'pause');
  const stream = '{"type":"session"}\n{"type":"parked","toolCallId":"call","toolName":"land"}\n';
  const r = spawnSync('python3', [join(adapter, 'stop-at-gap'), pause, 'sh', '-c', 'printf %s "$1"; touch "$2"; sleep .2', 'sh', stream, pause], { encoding: 'utf8', timeout: 5000 });
  assert.equal(r.status, 0);
  assert.equal(r.stdout, stream);
});

test('stop-at-gap journal query names outstanding wake tasks and fails closed for an unknown process', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'wake-journal-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const journal = join(dir, 'journal.sqlite');
  execFileSync('python3', ['-c', `import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    db.executescript('''
        CREATE TABLE instances(instance_id, process_id, started_at, disabled_at, pi_session_id);
        CREATE TABLE tasks(task_key, started_by_instance, completion_policy, ended_at);
        CREATE TABLE deliveries(delivery_key, task_key, wake_requested);
        CREATE TABLE delivery_events(delivery_key, event);
        INSERT INTO instances VALUES ('run', 123, 456, NULL, 'session'), ('previous', 99, 100, NULL, 'session'), ('other', 100, 100, NULL, 'other');
        INSERT INTO tasks VALUES ('owed', 'run', 'wake', NULL),
            ('prior-owed', 'previous', 'wake', NULL), ('other-owed', 'other', 'wake', NULL),
            ('observed', 'run', 'wake', 789), ('quiet', 'run', 'none', NULL);
        INSERT INTO deliveries VALUES ('delivery', 'observed', 1);
        INSERT INTO delivery_events VALUES ('delivery', 'observed');
    ''')`, journal]);
  const wakes = (pid) => JSON.parse(execFileSync('python3', [join(adapter, 'stop-at-gap'), '--owed-wakes', pid, '456'], {
    env: { ...process.env, PI_BACKGROUND_TASK_JOURNAL: journal }, encoding: 'utf8',
  }));
  assert.deepEqual(wakes('123'), ['owed', 'prior-owed']);
  assert.equal(wakes('124'), null);
});
