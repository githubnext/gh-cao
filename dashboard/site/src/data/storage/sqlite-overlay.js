import { readFileSync } from 'node:fs';
import { URL as NodeURL, pathToFileURL } from 'node:url';
import { createDebug } from '../../debug.js';

const debug = createDebug('sqlite-overlay');

/**
 * @param {import('node:sqlite').DatabaseSync} connection
 * @param {{ snapshotPath: string, snapshotRevision: string }} overlay
 */
export function attachSqliteOverlay(connection, overlay) {
  const uri = pathToFileURL(overlay.snapshotPath);
  uri.searchParams.set('mode', 'ro');
  connection.prepare('ATTACH DATABASE ? AS snapshot').run(uri.href);
  const metadata = connection.prepare(
    'SELECT snapshot_revision FROM main.__cao_overlay_metadata WHERE id=1'
  ).get();
  if (metadata?.snapshot_revision !== overlay.snapshotRevision) {
    debug({ event: 'revision-mismatch', status: 'rejected', metadataPresent: Boolean(metadata) });
    throw new Error('SQLite overlay does not match the deployed snapshot');
  }
  connection.exec(readFileSync(new NodeURL('../../../../../internal/dashboardquery/overlay.sql', import.meta.url), 'utf8'));
  debug({ event: 'attached', status: 'ok' });
}
