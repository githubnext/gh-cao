// Demo-only synchronous SHA-256 keeps seeded samples identical in Node and browser workers.
const constants = new Uint32Array(64);
const initial = new Uint32Array(8);
let primeCount = 0;
for (let candidate = 2; primeCount < 64; candidate++) {
  let prime = true;
  for (let divisor = 2; divisor * divisor <= candidate; divisor++) {
    if (candidate % divisor === 0) { prime = false; break; }
  }
  if (!prime) continue;
  if (primeCount < 8) initial[primeCount] = (Math.sqrt(candidate) % 1) * 0x100000000;
  constants[primeCount++] = (Math.cbrt(candidate) % 1) * 0x100000000;
}
const rotate = (value, count) => value >>> count | value << (32 - count);

export function demoSha256(value) {
  const bytes = new TextEncoder().encode(value);
  const padded = new Uint8Array(Math.ceil((bytes.length + 9) / 64) * 64);
  padded.set(bytes);
  padded[bytes.length] = 128;
  const view = new DataView(padded.buffer);
  view.setUint32(padded.length - 8, Math.floor(bytes.length / 0x20000000));
  view.setUint32(padded.length - 4, bytes.length * 8);
  const state = initial.slice();
  const words = new Uint32Array(64);
  for (let offset = 0; offset < padded.length; offset += 64) {
    for (let index = 0; index < 64; index++) {
      if (index < 16) words[index] = view.getUint32(offset + index * 4);
      else {
        const x = words[index - 15], y = words[index - 2];
        words[index] = words[index - 16] + (rotate(x, 7) ^ rotate(x, 18) ^ x >>> 3)
          + words[index - 7] + (rotate(y, 17) ^ rotate(y, 19) ^ y >>> 10);
      }
    }
    let [a, b, c, d, e, f, g, h] = state;
    for (let index = 0; index < 64; index++) {
      const first = (h + (rotate(e, 6) ^ rotate(e, 11) ^ rotate(e, 25))
        + (e & f ^ ~e & g) + constants[index] + words[index]) >>> 0;
      const second = ((rotate(a, 2) ^ rotate(a, 13) ^ rotate(a, 22)) + (a & b ^ a & c ^ b & c)) >>> 0;
      [a, b, c, d, e, f, g, h] = [(first + second) >>> 0, a, b, c, (d + first) >>> 0, e, f, g];
    }
    [a, b, c, d, e, f, g, h].forEach((word, index) => { state[index] += word; });
  }
  return [...state].map(word => word.toString(16).padStart(8, "0")).join("");
}
