/**
 * A public key as the 53 character base32 label mesh names use, the form a
 * DNS name can hold (pkg/cipher DNSLabel: RFC 4648 base32, lowercase, no
 * padding). Returns '' for anything that is not a 66 character hex key.
 */
export function pkLabel(hexKey: string): string {
  if (!/^[0-9a-fA-F]{66}$/.test(hexKey || '')) {
    return '';
  }
  const alphabet = 'abcdefghijklmnopqrstuvwxyz234567';
  let out = '';
  let bits = 0;
  let value = 0;
  for (let i = 0; i < hexKey.length; i += 2) {
    value = (value << 8) | parseInt(hexKey.substr(i, 2), 16);
    bits += 8;
    while (bits >= 5) {
      out += alphabet[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) {
    out += alphabet[(value << (5 - bits)) & 31];
  }

  return out;
}
