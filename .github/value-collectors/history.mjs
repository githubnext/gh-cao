const json = (value) => JSON.stringify(value, Object.keys(value).sort());

export function assertValueHistory(previous, packet) {
  if (!previous) return;
  if (previous.id !== packet.id || previous.contractDigest !== packet.contractDigest
    || Date.parse(packet.observedAt) <= Date.parse(previous.observedAt)
    || Date.parse(packet.windowStart) > Date.parse(previous.windowStart)
    || Date.parse(packet.windowEnd) < Date.parse(previous.windowEnd)) throw new Error('Frozen contract, capture or history conflict');
  const key = (fact) => JSON.stringify([fact['fact-kind'], fact['parent-id'] ?? '', fact['fact-id']]);
  const incoming = new Map(packet.facts.map((fact) => [key(fact), fact]));
  for (const old of previous.facts) {
    const kind = old['fact-kind'];
    if (!['contract', 'implementation', 'snapshot', 'snapshot-item', 'output'].includes(kind)
      && !kind.startsWith('implementation-')) continue;
    const next = incoming.get(key(old));
    if (!next) throw new Error('Accepted evidence history was removed');
    const left = { ...old }, right = { ...next };
    if (kind === 'contract') {
      for (const field of ['outputs-available', 'repository-state-available', 'checks-available']) {
        if (old[field] === true && next[field] !== true) throw new Error('Evidence availability was revoked');
        delete left[field]; delete right[field];
      }
      delete left['output-coverage']; delete right['output-coverage'];
    }
    if (kind === 'output' || kind === 'implementation') {
      if (old.origin !== next.origin && !(old.origin === 'corroborated' && next.origin === 'receipt-verified')) throw new Error('Accepted origin changed');
      delete left.origin; delete right.origin;
    }
    if (kind === 'output') { delete left.state; delete right.state; }
    if (json(left) !== json(right)) throw new Error('Immutable accepted evidence changed');
  }
}
