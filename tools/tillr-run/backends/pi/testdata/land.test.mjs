import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import landTool, { executeLand } from '../land.ts';
import noChangeTool, { executeNoChange } from '../no_change.ts';
import obstacleTool, { executeObstacle } from '../obstacle.ts';
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
  const forbidden = () => { throw new Error('unexpected wake query'); };
  const land = (id = 'call', wakes = () => [], answered, transcript) => executeLand(id, cwd, dir, base, wakes, answered, transcript);
  const offer = join(dir, 'landing-offer.json');
  const refuses = (attempt, reason) => {
    assert.throws(attempt, reason);
    assert.equal(existsSync(offer), false, 'a rejected call must not write an offer');
  };
  const change = () => { put('repo/in', 'change'); git('add', 'in'); commit(); return git('rev-parse', 'HEAD'); };
  return { dir, put, state, cwd, git, base, forbidden, land, offer, refuses, change };
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

test('land ignores old report-only refusals but honors content refusals', (t) => {
  const { land, offer, state, change, refuses, base } = landFixture(t);
  const commit = change();
  const report = { commit, summary: 'old summary', answer: 'Criterion c2 (report): missing results', report_only: true };
  const content = { commit, answer: 'Criterion c1 (content): missing test' };
  state({ refused: [report, content] });
  refuses(() => land('next'), /new commit is required[\s\S]*c1 \(content\)/);
  state({ refused: [report] });
  assert.equal(land('next').park, true);
  assert.deepEqual(JSON.parse(readFileSync(offer)), { call_id: 'next', commit, base, transcript: '' });
});

test('killed before the offer exists: replay writes the offer and parks', (t) => {
  const { put, land, offer, base, change } = landFixture(t);
  const head = change();
  put('repo/outside', 'uncommitted work');
  assert.throws(() => land(), /uncommitted paths[\s\S]*outside/);
  rmSync(join(offer, '..', 'repo', 'outside'));
  assert.equal(land().park, true);
  assert.deepEqual(JSON.parse(readFileSync(offer)), { call_id: 'call', commit: head, base, transcript: '' });
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

// Pi's agent loop settles a tool call with `execution.then(...)` on whatever execute returned,
// so a plain object comes back as "execution.then is not a function" after the offer is
// already on disk. Drive the registered tools through that same settlement.
const settle = (execution) => execution.then((result) => ({ kind: 'result', result }), (error) => ({ kind: 'failed', error }));

test('registered land and obstacle settle through pi as promises, parking and refusing', async (t) => {
  const { cwd, base, change, offer } = landFixture(t);
  const dir = join(cwd, '..');
  const old = { ...process.env };
  t.after(() => { process.env = old; });
  writeFileSync(join(dir, 'python3'), '#!/bin/sh\nprintf "[]\\n"\n', { mode: 0o700 });
  Object.assign(process.env, { YOLO_DURABLE_DIR: dir, TILLR_GOAL_BASE: base, BOARD_DRAIN_BRIEF: join(dir, 'brief.md'), PATH: `${dir}:${old.PATH}` });
  const ctx = { cwd, sessionManager: { getSessionFile: () => '', getBranch: () => [] } };
  const tools = {};
  landTool({ on() {}, registerTool: value => { tools.land = value; } });
  obstacleTool({ on() {}, registerTool: value => { tools.obstacle = value; } });
  let settled = await settle(tools.land.execute('call', {}, undefined, undefined, ctx));
  assert.equal(settled.kind, 'failed');
  assert.match(settled.error.message, /no commit/);
  change();
  settled = await settle(tools.land.execute('call', {}, undefined, undefined, ctx));
  assert.equal(settled.kind, 'result');
  assert.equal(settled.result.park, true);
  rmSync(offer);
  settled = await settle(tools.obstacle.execute('call', { kind: 'owner', question: 'which?' }, undefined, undefined, ctx));
  assert.equal(settled.kind, 'result');
  assert.equal(settled.result.park, true);
  delete process.env.TILLR_GOAL_BASE;
  settled = await settle(tools.obstacle.execute('call', { kind: 'owner', question: 'which?' }, undefined, undefined, ctx));
  assert.equal(settled.kind, 'failed');
});

test('a retired fence obstacle is not offered', (t) => {
  const { cwd, base, refuses, forbidden } = landFixture(t);
  const paths = 'tests/new.go, refs/heads/patch/pi, .forks/lanes/<fork-lane>/pi/';
  refuses(() => executeObstacle('call', { kind: 'fence', paths, evidence: 'needed' }, cwd, join(cwd, '..'), base, forbidden),
    /Invalid obstacle kind/);
});

test('an offer whose call already has a result is stale and does not strand the run', (t) => {
  const { land, offer, change, put } = landFixture(t);
  const head = change();
  const dir = join(offer, '..');
  const stale = { call_id: 'failed', commit: head, base: 'base', summary: 'summary', transcript: '' };
  put('landing-offer.json', JSON.stringify(stale));
  assert.throws(() => land('next', () => [], () => false), /open: failed/);
  assert.equal(land('next', () => [], id => id === 'failed').park, true);
  assert.equal(JSON.parse(readFileSync(offer)).call_id, 'next');
  put('landing-offer.json', JSON.stringify(stale));
  const report = (answered) => executeObstacle('obstacle', { kind: 'owner', question: 'which?' }, join(dir, 'repo'), dir, 'base', () => [], answered);
  assert.throws(() => report(() => false), /ending call is open: failed/);
  assert.equal(report(id => id === 'failed').park, true);
  assert.equal(JSON.parse(readFileSync(offer)).call_id, 'obstacle');
});

test('registered land passes the session file and never writes landing-transcript', async (t) => {
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
  await tool.execute('call', {},  undefined, undefined, ctx);
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
  const brief = '- **Target:** test (lane worktree)\n' + ['Header', 'Task', 'Scope and boundaries', 'Seats', 'Settled decisions', 'Facts carried', 'Method', 'Verification commands', 'Report shape', 'Stop conditions'].map(heading => `## ${heading}\n`).join('');
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
  const chosen = footer.replace('## Report shape', '## Finishing').replace('--no-extensions', `-e\n${adapter}land.ts\n-e\n${adapter}obstacle.ts\n--no-extensions`);
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
  put('brief.md', '- **Target:** test (lane worktree)\n' + ['Header', 'Task', 'Scope and boundaries', 'Seats', 'Settled decisions', 'Facts carried', 'Method', 'Verification commands', 'Finishing', 'Stop conditions'].map(heading => `## ${heading}\n`).join(''));
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

test('resumed session is not held by a finished wake task from a dead process, but still held by running or live-process wakes', async (t) => {
  const { cwd, base, change, offer } = landFixture(t);
  const dir = join(cwd, '..');
  const old = { ...process.env };
  t.after(() => { process.env = old; });
  change();
  const journal = join(dir, 'journal.sqlite');
  const now = Date.now();
  const priorStart = now - 60_000;
  const runSql = (sql, ...args) => execFileSync('python3', ['-c', `import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    db.execute(sys.argv[2], sys.argv[3:])`, journal, sql, ...args]);
  execFileSync('python3', ['-c', `import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    db.executescript('''
        CREATE TABLE instances(instance_id, process_id, started_at, disabled_at, pi_session_id);
        CREATE TABLE tasks(task_key, started_by_instance, completion_policy, ended_at);
        CREATE TABLE deliveries(delivery_key, task_key, wake_requested);
        CREATE TABLE delivery_events(delivery_key, event);
    ''')
    db.execute("INSERT INTO instances VALUES ('previous', ?, ?, NULL, 'session')", (int(sys.argv[2]), int(sys.argv[3])))
    db.execute("INSERT INTO instances VALUES ('current', ?, ?, NULL, 'session')", (int(sys.argv[4]), int(sys.argv[5])))
    # Earlier process died after these wake tasks finished, before observing completion.
    db.execute("INSERT INTO tasks VALUES ('prior-ended-no-delivery', 'previous', 'wake', ?)", (int(sys.argv[3]) + 500,))
    db.execute("INSERT INTO tasks VALUES ('prior-ended-enqueued', 'previous', 'wake', ?)", (int(sys.argv[3]) + 600,))
    db.execute("INSERT INTO deliveries VALUES ('d-prior', 'prior-ended-enqueued', 1)")
    db.execute("INSERT INTO delivery_events VALUES ('d-prior', 'enqueued')")
`, journal, String(process.pid + 1), String(priorStart), String(process.pid), String(now)]);

  Object.assign(process.env, {
    YOLO_DURABLE_DIR: dir,
    TILLR_GOAL_BASE: base,
    PI_BACKGROUND_TASK_JOURNAL: journal,
  });
  const ctx = { cwd, sessionManager: { getSessionFile: () => '', getBranch: () => [] } };
  const tools = {};
  landTool({ on() {}, registerTool: value => { tools.land = value; } });
  obstacleTool({ on() {}, registerTool: value => { tools.obstacle = value; } });

  // Criterion 5: land and obstacle are no longer refused for the dead process's finished tasks.
  let settled = await settle(tools.land.execute('call-land', {}, undefined, undefined, ctx));
  assert.equal(settled.kind, 'result', settled.error?.message);
  assert.equal(settled.result.park, true);
  rmSync(offer);

  settled = await settle(tools.obstacle.execute('call-obstacle', { kind: 'owner', question: 'which?' }, undefined, undefined, ctx));
  assert.equal(settled.kind, 'result', settled.error?.message);
  assert.equal(settled.result.park, true);
  rmSync(offer);

  // Criterion 6a: a wake task that finished in the live process without being observed still holds the session.
  runSql("INSERT INTO tasks VALUES ('live-ended-unobserved', 'current', 'wake', ?)", String(now + 1));
  settled = await settle(tools.land.execute('call-land-2', {}, undefined, undefined, ctx));
  assert.equal(settled.kind, 'failed');
  assert.match(settled.error.message, /live-ended-unobserved/);
  settled = await settle(tools.obstacle.execute('call-obstacle-2', { kind: 'owner', question: 'which?' }, undefined, undefined, ctx));
  assert.equal(settled.kind, 'failed');
  assert.match(settled.error.message, /live-ended-unobserved/);
  runSql("DELETE FROM tasks WHERE task_key = 'live-ended-unobserved'");

  // Criterion 6b: a wake task still running (even from the earlier process) still holds the session.
  runSql("INSERT INTO tasks VALUES ('prior-still-running', 'previous', 'wake', NULL)");
  settled = await settle(tools.land.execute('call-land-3', {}, undefined, undefined, ctx));
  assert.equal(settled.kind, 'failed');
  assert.match(settled.error.message, /prior-still-running/);
  settled = await settle(tools.obstacle.execute('call-obstacle-3', { kind: 'owner', question: 'which?' }, undefined, undefined, ctx));
  assert.equal(settled.kind, 'failed');
  assert.match(settled.error.message, /prior-still-running/);
});

test('obstacle accepts all six kinds, rejects missing required fields, and replays answers', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'obstacle-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const cwd = join(dir, 'repo');
  mkdirSync(cwd);
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim();
  git('init', '-q');
  writeFileSync(join(cwd, 'in'), 'base');
  git('add', 'in');
  git('-c', 'user.name=Test', '-c', 'user.email=test@example.org', 'commit', '-qm', 'test');
  const base = git('rev-parse', 'HEAD');
  const put = (name, data) => writeFileSync(join(dir, name), data);
  const offer = join(dir, 'landing-offer.json');
  const run = (args, { id = 'call', wakes = () => [], answered, transcript = '', attempted } = {}) =>
    executeObstacle(id, args, cwd, dir, base, wakes, answered, transcript, attempted);

  for (const args of [
    { kind: 'definition', criteria: 'c1', evidence: 'contradicts tree' },
    { kind: 'trunk', checks: 'go-build', base: '0123456789ab' },
    { kind: 'tooling', command: 'go test ./...', evidence: 'timed out', paths: 'tools/a_test.go' },
    { kind: 'seat', tried: 'two designs', missing: 'cannot keep both locks' },
    { kind: 'owner', question: 'which option should we ship?' },
  ]) {
    rmSync(offer, { force: true });
    const res = run(args, { attempted: () => true });
    assert.equal(res.park, true, args.kind);
    const recorded = JSON.parse(readFileSync(offer, 'utf8'));
    assert.equal(recorded.tool, 'obstacle');
    assert.equal(recorded.base, base);
    assert.equal(recorded.obstacle.kind, args.kind);
  }
  rmSync(offer, { force: true });

  for (const [args, pattern] of [
    [{ kind: 'unknown' }, /Invalid obstacle kind/],
    [{ kind: 'fence', paths: 'tools/a.go', evidence: 'outside' }, /Invalid obstacle kind/],
    [{ kind: 'definition', criteria: 'c1' }, /missing required field.*evidence/],
    [{ kind: 'trunk', checks: 'go-build' }, /missing required field.*base/],
    [{ kind: 'tooling', command: 'go test', evidence: 'hung' }, /missing required field.*paths/],
    [{ kind: 'seat', missing: 'something' }, /missing required field.*tried/],
    [{ kind: 'owner' }, /missing required field.*question/],
  ]) {
    assert.throws(() => run(args, { attempted: () => true }), pattern);
    assert.equal(existsSync(offer), false);
  }
  assert.throws(() => run({ kind: 'seat', tried: 'read files', missing: 'impl' }), /names no attempt/);
  assert.equal(existsSync(offer), false);

  const answer = { call_id: 'call', kind: 'obstacle-answer', text: 'Design answer commit: abc' };
  put('landing-answer.json', JSON.stringify(answer));
  assert.deepEqual(run({ kind: 'definition', criteria: 'c1', evidence: 'e' }), {
    content: [{ type: 'text', text: answer.text }],
    details: answer,
    isError: true,
  });

  let registered;
  obstacleTool({ registerTool: (value) => { registered = value; } });
  assert.equal(registered.name, 'obstacle');
  assert.deepEqual(registered.parameters.required, ['kind']);
  assert.deepEqual(registered.parameters.properties.kind.enum, ['definition', 'owner', 'seat', 'tooling', 'trunk']);
});

test('land refuses committed unformatted Go files with their names until formatted', (t) => {
  const { cwd, git, land, offer, refuses } = landFixture(t);
  const repoRoot = fileURLToPath(new URL('../../../../../', import.meta.url));
  writeFileSync(join(cwd, '.justfile'), readFileSync(join(repoRoot, '.justfile')));
  mkdirSync(join(cwd, 'tools/gate/checks'), { recursive: true });
  writeFileSync(join(cwd, 'tools/gate/checks/format.json'), readFileSync(join(repoRoot, 'tools/gate/checks/format.json')));
  mkdirSync(join(cwd, 'tools/example'), { recursive: true });
  writeFileSync(join(cwd, 'tools/example/unformatted.go'), 'package example\nfunc bad(){println(1)}\n');
  git('add', '.justfile', 'tools/gate/checks/format.json', 'tools/example/unformatted.go');
  git('-c', 'user.name=Test', '-c', 'user.email=test@example.org', 'commit', '-qm', 'unformatted');
  refuses(() => land(), /tools\/example\/unformatted\.go/);
  execFileSync('just', ['format'], { cwd });
  git('add', 'tools/example/unformatted.go');
  git('-c', 'user.name=Test', '-c', 'user.email=test@example.org', 'commit', '-q', '--amend', '--no-edit');
  assert.equal(land().park, true);
  assert.equal(existsSync(offer), true);
});

test('no_change validates clean base and finding, parks offer, and rejects repeats', (t) => {
  const { dir, cwd, base, git, offer, put } = landFixture(t);
  const run = (args, id = 'nc-1') => executeNoChange(id, args, cwd, dir, base, () => [], () => false, '/t.jsonl');
  assert.throws(() => run({ cause: 'c' }), /missing required field.*evidence/);
  const finding = { cause: 'sibling fix', evidence: 'go test passed', why_no_change: 'already green' };
  assert.equal(run(finding).park, true);
  const written = JSON.parse(readFileSync(offer, 'utf8'));
  assert.deepEqual(written, { call_id: 'nc-1', tool: 'no_change', base, finding, transcript: '/t.jsonl' });
  rmSync(offer, { force: true });

  put('landing-state.json', JSON.stringify({ refused: [{ tool: 'no_change', base, finding, answer: 'need proof' }] }));
  assert.throws(() => run(finding, 'nc-2'), /already refused/);
  rmSync(join(dir, 'landing-state.json'), { force: true });

  writeFileSync(join(cwd, 'dirty.txt'), 'x\n');
  assert.throws(() => run(finding, 'nc-3'), /not clean/);
  git('add', 'dirty.txt');
  git('-c', 'user.name=Test', '-c', 'user.email=test@example.org', 'commit', '-qm', 'change');
  assert.throws(() => run(finding, 'nc-4'), /commit exists at HEAD/);

  let registered;
  noChangeTool({ registerTool: (v) => { registered = v; } });
  assert.equal(registered.name, 'no_change');
});
