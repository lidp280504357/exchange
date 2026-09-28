// Prints the TOTP code (RFC 6238: SHA-1, 30 s, 6 digits) of a base32
// secret, for scripts/e2e/totp.sh:
//   node totp.mjs <secret> [steps ahead]
import { createHmac } from "node:crypto";

const [secret, ahead = "0"] = process.argv.slice(2);
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
let bits = "";
for (const c of secret.replace(/=+$/, "").toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, "0");
const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
const step = BigInt(Math.floor(Date.now() / 30000) + Number(ahead));
const msg = Buffer.alloc(8);
msg.writeBigUInt64BE(step);
const mac = createHmac("sha1", key).update(msg).digest();
const off = mac[mac.length - 1] & 0x0f;
const bin = mac.readUInt32BE(off) & 0x7fffffff;
console.log(String(bin % 1000000).padStart(6, "0"));
