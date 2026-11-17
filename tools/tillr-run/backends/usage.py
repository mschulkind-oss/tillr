"""Token kinds and streaming clocks; absent facts are omitted, never zero.

Latency is start to the first nonempty text/thinking delta. Throughput is output
 tokens per second from that delta to assistant completion, excluding tools. Pi's
message_end and Claude's message_stop identify completion; snapshots do not.
"""
import datetime

KINDS = ('input', 'output', 'cache_read', 'cache_write')
FIELDS = {
    'pi': ('input', 'output', 'cacheRead', 'cacheWrite'),
    'claude': ('input_tokens', 'output_tokens', 'cache_read_input_tokens', 'cache_creation_input_tokens'),
}


def stamp(record):
    value = record.get('timestamp')
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return value
    if isinstance(value, str):
        try:
            dt = datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))
            if dt.tzinfo:
                return dt.timestamp() * 1000
        except ValueError:
            pass
    return None


def counters(usage, backend, model):
    out = {'model': model or None}
    for key, field in zip(KINDS, FIELDS[backend]):
        value = (usage or {}).get(field)
        if isinstance(value, (int, float)) and not isinstance(value, bool) and value >= 0:
            out[key] = int(value)
    # Retain the reported total as well as kinds: Pi may report reasoning in
    # addition to these four kinds, so recomputing model totals would lose it.
    total = (usage or {}).get('totalTokens') if backend == 'pi' else None
    if isinstance(total, (int, float)) and not isinstance(total, bool) and total >= 0:
        out['tokens'] = int(total)
    else:
        fields = list(FIELDS[backend]) + (['reasoning'] if backend == 'pi' else [])
        values = [(usage or {}).get(field) for field in fields]
        values = [v for v in values if isinstance(v, (int, float)) and not isinstance(v, bool) and v >= 0]
        if values:
            out['tokens'] = int(sum(values))
    return out


def summed(rows):
    out = {}
    # A partially recorded counter is absent, not a misleading complete sum.
    for kind in KINDS + ('tokens',):
        if rows and all(kind in row for row in rows):
            out[kind] = sum(row[kind] for row in rows)
    return out


def details(records, backend):
    states, by_id = [], {}
    active = None
    model = None
    run_start = stamp(records[0]) if records else None
    run_first = {'seen': False, 'at': None}

    def new_state(at=None, key=None):
        state = {'start': at, 'first_seen': False, 'first': None, 'end': None,
                 'id': key or f'turn-{len(states) + 1}', 'counters': None}
        states.append(state)
        if key:
            by_id[key] = state
        return state

    def first_delta(state, at):
        # Latch presence independently of the clock: an unclocked first delta
        # cannot be replaced by a later clocked one, per turn or for the run.
        if not run_first['seen']:
            run_first.update(seen=True, at=at)
        if not state['first_seen']:
            state.update(first_seen=True, first=at)

    for index, record in enumerate(records):
        kind, at = record.get('type'), stamp(record)
        message = record.get('message') or {}
        if kind == 'system' and record.get('subtype') == 'init':
            model = record.get('model')
        if backend == 'pi':
            if kind == 'turn_start':
                active = new_state(at)
            elif kind == 'message_start' and message.get('role') == 'assistant':
                if active is None:
                    active = new_state(at)
                if message.get('id'):
                    active['id'] = message['id']
            elif kind == 'message_update':
                delta = record.get('assistantMessageEvent') or {}
                if delta.get('type') in ('text_delta', 'thinking_delta') and delta.get('delta'):
                    if active is None:
                        active = new_state()
                    first_delta(active, at)
            elif kind == 'message_end' and message.get('role') == 'assistant':
                if active is None:
                    active = new_state()
                active['end'] = at
                active['completion_id'] = message.get('id')
            elif kind == 'turn_end':
                if active is None:
                    active = new_state()
                active['counters'] = counters(message.get('usage'), backend, message.get('model'))
                if message.get('id'):
                    active['id'] = message['id']
                    if active.get('completion_id') not in (None, message['id']):
                        active['end'] = None
                active = None
        else:
            if kind == 'stream_event':
                ev = record.get('event') or {}
                if ev.get('type') == 'message_start':
                    active = new_state(at, (ev.get('message') or {}).get('id'))
                elif ev.get('type') == 'content_block_delta':
                    delta = ev.get('delta') or {}
                    if delta.get('text') or delta.get('thinking'):
                        if active is None:
                            active = new_state()
                        first_delta(active, at)
                elif ev.get('type') == 'message_stop' and active is not None:
                    active['end'] = at
                continue
            if kind == 'assistant':
                key = message.get('id')
                state = by_id.get(key) if key else None
                if state is None:
                    if active is not None and (not key or active['id'].startswith('turn-') or active['id'] == key):
                        state = active
                    else:
                        state = new_state(key=key or f'line-{index}')
                    if key:
                        state['id'] = key
                        by_id[key] = state
                # Repeated blocks update one message, never add its usage twice.
                # Keep the streaming state until message_stop, not the first block.
                if isinstance(message.get('usage'), dict):
                    state['counters'] = counters(message['usage'], backend, message.get('model') or model)

    turns = []
    for state in states:
        row = dict(state['counters'] or {})
        if not any(k in row for k in KINDS + ('tokens',)):
            continue
        row['id'] = state['id']
        start, first, end = state['start'], state['first'], state['end']
        if start is not None:
            row['started_at_ms'] = start
        if end is not None:
            row['finished_at_ms'] = end
        if first is not None:
            row['first_output_at_ms'] = first
        if start is not None and first is not None and first >= start:
            row['first_output_ms'] = first - start
        if first is not None and end is not None and end > first and 'output' in row:
            row['output_tokens_per_second'] = row['output'] * 1000 / (end - first)
        turns.append(row)
    models = []
    for name in dict.fromkeys(t['model'] for t in turns):
        rows = [t for t in turns if t['model'] == name]
        models.append({'model': name, **summed(rows)})
    # A single-model Claude result is authoritative. Do not infer how a mixed
    # run's result total divides between models: retain the message breakdown.
    if backend == 'claude' and len(models) <= 1:
        final = next((r for r in reversed(records) if r.get('type') == 'result'), None)
        if final and final.get('usage'):
            measured = counters(final['usage'], backend, models[0]['model'] if models else model)
            if any(k in measured for k in KINDS + ('tokens',)):
                models = [measured]
    if not models:
        return None
    out = {'models': models, 'turns': turns}
    first = run_first['at']
    if run_start is not None and first is not None and first >= run_start:
        out['first_output_ms'] = first - run_start
    if turns and len(turns) == len(states) and all('output_tokens_per_second' in t for t in turns) and all('output' in m for m in models):
        seconds = sum((t['finished_at_ms'] - t['first_output_at_ms']) / 1000 for t in turns)
        if seconds > 0:
            out['output_tokens_per_second'] = sum(m['output'] for m in models) / seconds
    return out
