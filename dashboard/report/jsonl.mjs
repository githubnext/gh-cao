import { iterateJsonlEvents, readJsonlFileEvents } from './vendor/gh-aw/jsonl_helpers.cjs';

function* records(events, source) {
  for (const event of events) {
    if (event.type === 'invalid') {
      if (event.reason === 'blank') continue;
      throw new SyntaxError(`${source}:${event.lineNumber}: ${event.error.message}; record contents omitted`);
    }
    yield event;
  }
}

export function parseJsonlRecords(content, source = 'JSONL input') {
  return records(iterateJsonlEvents(content), source);
}

export function readJsonlRecords(filePath) {
  return records(readJsonlFileEvents(filePath), filePath);
}

export function* jsonlRecords(paths) {
  for (const filePath of paths) yield* readJsonlRecords(filePath);
}
