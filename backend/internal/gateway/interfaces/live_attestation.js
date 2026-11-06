const addon = require(process.env.BOS_DEVICECHECK_ADDON);
const signals = JSON.parse(process.env.BOS_DEVICECHECK_SIGNALS);
const bundle = process.env.BOS_DEVICECHECK_BUNDLE;
function head(major, size) {
  if (size < 24) return Buffer.from([major + size]);
  if (size < 256) return Buffer.from([major + 24, size]);
  if (size < 65536) { const b = Buffer.alloc(3); b[0] = major + 25; b.writeUInt16BE(size, 1); return b; }
  const b = Buffer.alloc(5); b[0] = major + 26; b.writeUInt32BE(size, 1); return b;
}
function integer(value) { return head(0, value); }
function text(value) { const b = Buffer.from(value, 'utf8'); return Buffer.concat([head(96, b.length), b]); }
function number(value) {
  if (Number.isSafeInteger(value) && value >= 0) return integer(value);
  const b = Buffer.alloc(9); b[0] = 251; b.writeDoubleBE(value, 1); return b;
}
function array(items) { return Buffer.concat([head(128, items.length), ...items]); }
function map(entries) { return Buffer.concat([head(160, entries.length), ...entries.flatMap(([key, value]) => [integer(key), value])]); }
function pair(key, value) { return Buffer.concat([text(key), text(value)]); }
(async () => {
  const result = await addon.generateToken();
  if (!result?.supported || !result.tokenBase64) throw Error('devicecheck unavailable');
  const fingerprint = map([
    [0, integer(signals.schemaVersion)],
    [1, array(signals.preferredLanguages.map(text))],
    [2, text(signals.locale)],
    [3, text(signals.timezone)],
    [4, integer(signals.screenSizeSum)],
    [5, number(signals.screenScale)],
    [6, text(signals.appSessionId)]
  ]);
  const fields = [pair('token', result.tokenBase64), pair('bundle_id', bundle), Buffer.concat([text('f'), head(64, fingerprint.length), fingerprint])];
  if (result.latencyMs != null) fields.push(Buffer.concat([text('t'), number(result.latencyMs)]));
  const binary = Buffer.concat([head(160, fields.length), ...fields]);
  process.stdout.write(JSON.stringify({v: 1, s: 0, t: 'v1.' + binary.toString('base64url')}));
})().catch(() => { process.exitCode = 1; });
