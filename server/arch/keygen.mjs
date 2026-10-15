// Makes the Mango Arch signing key. The private key goes to the server as a
// secret; the public key is built into MangoMan (arch.Key). Run once:
//   node keygen.mjs
const k = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);
const b64 = (b) => Buffer.from(b).toString("base64");
console.log("ARCH_PRIVATE_KEY (secret, for the server):", b64(await crypto.subtle.exportKey("pkcs8", k.privateKey)));
console.log("Public key (for MangoMan builds):", b64(await crypto.subtle.exportKey("raw", k.publicKey)));
