package docx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rc4"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"hash"
	"strings"
	"unicode/utf16"
)

// offcrypto.go implements MS-OFFCRYPTO Standard and Agile encryption
// decryption for OOXML packages, just enough to recover the inner zip
// of a password-protected .docx. References: [MS-OFFCRYPTO] 2.x.
//
// Standard encryption (Office 2007+):
//
//	EncryptionInfo header (40 bytes) + EncryptionHeader + EncryptionVerifier.
//	Cipher = AES-128 ECB, hashed via 50000 SHA-1 iterations.
//
// Agile encryption (Office 2010+):
//
//	EncryptionInfo is XML; cipher params live in <keyData>; password is
//	verified through a key-encrypting key derived per spec.
//
// The implementation prioritizes Standard (the common variant
// produced by "Encrypt with Password" in Word). Agile detection
// returns ErrAgileUnsupported when we can't fully decrypt.

// ErrWrongPassword is returned when the supplied password fails the
// EncryptionVerifier check.
var ErrWrongPassword = errors.New("offcrypto: wrong password")

// ErrAgileUnsupported is returned when the container uses Agile
// encryption and the supplied parameters fall outside our
// implementation's coverage (e.g. non-AES cipher or non-SHA512 hash).
var ErrAgileUnsupported = errors.New("offcrypto: agile encryption parameters unsupported")

// ErrLegacyXORObfuscation is returned when the CFB container appears
// to use the Word 95/97 XOR obfuscation scheme. XOR obfuscation only
// exists on the legacy binary `.doc` format (which this library doesn't
// otherwise consume), but is surfaced here so callers handling mixed
// inputs can detect and reject legacy files cleanly.
var ErrLegacyXORObfuscation = errors.New("offcrypto: legacy XOR obfuscation (Word 95/97) not supported on .docx")

// DecryptOOXML opens a CFB-wrapped, password-encrypted .docx blob and
// returns the inner zip bytes. password is the user password.
func DecryptOOXML(data []byte, password string) ([]byte, error) {
	c, err := openCFB(data)
	if err != nil {
		return nil, err
	}
	infoBytes, ok := c.readStream("EncryptionInfo")
	if !ok {
		return nil, errors.New("offcrypto: no EncryptionInfo stream")
	}
	pkgBytes, ok := c.readStream("EncryptedPackage")
	if !ok {
		return nil, errors.New("offcrypto: no EncryptedPackage stream")
	}
	if len(infoBytes) < 4 {
		return nil, errors.New("offcrypto: EncryptionInfo too small")
	}
	major := binary.LittleEndian.Uint16(infoBytes[0:])
	minor := binary.LittleEndian.Uint16(infoBytes[2:])
	switch {
	case major == 4 && minor == 4:
		return decryptAgile(infoBytes, pkgBytes, password)
	case (major == 3 || major == 4) && minor == 2:
		// Distinguish RC4-CryptoAPI (Office 2002–2003 with .docx wrapper)
		// from AES-Standard by the fAES flag in EncryptionFlags. Bit 5
		// (0x20) is set when the package was encrypted with AES.
		if len(infoBytes) >= 8 {
			flags := binary.LittleEndian.Uint32(infoBytes[4:])
			const fAES = 0x20
			if flags&fAES == 0 {
				return decryptRC4CryptoAPI(infoBytes, pkgBytes, password)
			}
		}
		return decryptStandard(infoBytes, pkgBytes, password)
	case (major == 1 || major == 2) && minor == 1:
		// Office 2002 / 2003 native: same RC4 CryptoAPI parameters as
		// 3.2/4.2 but with a different version marker. Parsed by the
		// same path; the EncryptionHeader / EncryptionVerifier layout
		// is identical.
		return decryptRC4CryptoAPI(infoBytes, pkgBytes, password)
	default:
		return nil, errors.New("offcrypto: unsupported encryption version")
	}
}

// decryptRC4CryptoAPI implements MS-OFFCRYPTO §2.3.5 RC4 CryptoAPI
// Encryption. Layout mirrors Standard Encryption (header, verifier) but:
//   - Cipher is RC4 (variable key length, typically 128 bits).
//   - Hash function is SHA-1.
//   - The EncryptedPackage is split into 4 KiB blocks; each block is
//     decrypted with a per-block RC4 key derived from
//     SHA1(baseDerivedKey || blockNumber).
//
// This covers the most common pre-2007 "encrypt with password" path Word
// took before Office 2007 made AES the default.
func decryptRC4CryptoAPI(info, pkg []byte, password string) ([]byte, error) {
	if len(info) < 12+32 {
		return nil, errors.New("rc4: EncryptionInfo too small")
	}
	headerSize := binary.LittleEndian.Uint32(info[8:])
	off := 12
	if off+int(headerSize) > len(info) {
		return nil, errors.New("rc4: header overflows")
	}
	hdr := info[off : off+int(headerSize)]
	off += int(headerSize)
	keyBits := binary.LittleEndian.Uint32(hdr[20:])
	if keyBits == 0 {
		keyBits = 40
	}
	keyBytes := int(keyBits) / 8
	if off+16+16+4+4+16 > len(info) {
		return nil, errors.New("rc4: verifier overflows")
	}
	saltSize := binary.LittleEndian.Uint32(info[off:])
	off += 4
	if saltSize != 16 {
		return nil, errors.New("rc4: unexpected salt size")
	}
	salt := info[off : off+16]
	off += 16
	encVerifier := info[off : off+16]
	off += 16
	verifierHashSize := binary.LittleEndian.Uint32(info[off:])
	off += 4
	if int(verifierHashSize) > len(info)-off {
		return nil, errors.New("rc4: verifier hash overflows")
	}
	encVerifierHash := info[off : off+int(verifierHashSize)]

	// Verifier is encrypted with the per-block key for block 0.
	baseKey := deriveRC4Key(password, salt, keyBytes)
	blockKey := deriveRC4BlockKey(baseKey, 0, keyBytes)

	stream, err := rc4.NewCipher(blockKey)
	if err != nil {
		return nil, err
	}
	verifier := make([]byte, 16)
	stream.XORKeyStream(verifier, encVerifier)
	// Verifier hash is also RC4'd with the same block-0 key, continuing
	// the keystream from after the verifier — but per MS-OFFCRYPTO the
	// hash sits in a fresh RC4 stream offset; the safe approach is to
	// reset the cipher and skip 16 keystream bytes.
	stream, _ = rc4.NewCipher(blockKey)
	drop := make([]byte, 16)
	stream.XORKeyStream(drop, drop)
	verHash := make([]byte, len(encVerifierHash))
	stream.XORKeyStream(verHash, encVerifierHash)
	want := sha1.Sum(verifier)
	if !bytesEq(want[:], verHash[:20]) {
		return nil, ErrWrongPassword
	}

	// Decrypt the package. First 8 bytes = original size; the rest is the
	// encrypted stream split into 512-byte blocks each with its own per-
	// block RC4 key. (RC4 CryptoAPI uses 512-byte blocks, NOT 4 KiB.)
	if len(pkg) < 8 {
		return nil, errors.New("rc4: package too small")
	}
	origSize := int(binary.LittleEndian.Uint64(pkg[:8]))
	cipherText := pkg[8:]
	plain := make([]byte, len(cipherText))
	const blockSize = 512
	for blockNum, pos := uint32(0), 0; pos < len(cipherText); blockNum, pos = blockNum+1, pos+blockSize {
		key := deriveRC4BlockKey(baseKey, blockNum, keyBytes)
		stream, err := rc4.NewCipher(key)
		if err != nil {
			return nil, err
		}
		end := pos + blockSize
		if end > len(cipherText) {
			end = len(cipherText)
		}
		stream.XORKeyStream(plain[pos:end], cipherText[pos:end])
	}
	if origSize > 0 && origSize <= len(plain) {
		plain = plain[:origSize]
	}
	return plain, nil
}

// deriveRC4Key produces the base key for RC4 CryptoAPI per §2.3.5.
// Uses the same 50000-round SHA-1 derivation as Standard Encryption
// minus the AES-specific X1/X2 mix.
func deriveRC4Key(password string, salt []byte, keyBytes int) []byte {
	pwd := utf16LE(password)
	h := sha1.New()
	h.Write(salt)
	h.Write(pwd)
	digest := h.Sum(nil)
	for i := uint32(0); i < 50000; i++ {
		var ib [4]byte
		binary.LittleEndian.PutUint32(ib[:], i)
		h.Reset()
		h.Write(ib[:])
		h.Write(digest)
		digest = h.Sum(nil)
	}
	// No X1/X2 mix for RC4 — just the truncated derived hash.
	if keyBytes > len(digest) {
		keyBytes = len(digest)
	}
	return digest[:keyBytes]
}

// deriveRC4BlockKey produces the per-block RC4 key by hashing
// (baseKey || blockNumber). Per spec the result is truncated to the
// configured key length, then for keys < 16 bytes padded with zeros up
// to 16 bytes to match the RC4 cipher's preferred state size.
func deriveRC4BlockKey(baseKey []byte, blockNum uint32, keyBytes int) []byte {
	h := sha1.New()
	h.Write(baseKey)
	var ib [4]byte
	binary.LittleEndian.PutUint32(ib[:], blockNum)
	h.Write(ib[:])
	d := h.Sum(nil)
	if keyBytes > len(d) {
		keyBytes = len(d)
	}
	return d[:keyBytes]
}

// decryptStandard implements MS-OFFCRYPTO §2.3.4.5 Standard Encryption.
func decryptStandard(info, pkg []byte, password string) ([]byte, error) {
	if len(info) < 12+32 {
		return nil, errors.New("standard: EncryptionInfo too small")
	}
	// Skip 4-byte version + 4-byte flags + 4-byte header size.
	headerSize := binary.LittleEndian.Uint32(info[8:])
	off := 12
	if off+int(headerSize) > len(info) {
		return nil, errors.New("standard: header overflows EncryptionInfo")
	}
	hdr := info[off : off+int(headerSize)]
	off += int(headerSize)
	// EncryptionHeader fields we need:
	//   AlgID at hdr[8:12], KeySize at hdr[20:24].
	keyBits := binary.LittleEndian.Uint32(hdr[20:])
	if keyBits == 0 {
		// Some writers omit; default to 128.
		keyBits = 128
	}
	keyBytes := int(keyBits) / 8
	// EncryptionVerifier (next 16+4+32+4+32 bytes minimum):
	//   Salt size (uint32) | Salt (16) | EncVerifier (16) |
	//   VerifierHashSize (uint32) | EncVerifierHash (32)
	if off+16+16+4+4+32 > len(info) {
		return nil, errors.New("standard: verifier overflows")
	}
	saltSize := binary.LittleEndian.Uint32(info[off:])
	off += 4
	if saltSize != 16 {
		return nil, errors.New("standard: unexpected salt size")
	}
	salt := info[off : off+16]
	off += 16
	encVerifier := info[off : off+16]
	off += 16
	verifierHashSize := binary.LittleEndian.Uint32(info[off:])
	off += 4
	encVerifierHash := info[off : off+int(verifierHashSize)]

	key := deriveStandardKey(password, salt, keyBytes)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	verifier := make([]byte, 16)
	block.Decrypt(verifier, encVerifier)
	verHash := make([]byte, len(encVerifierHash))
	for i := 0; i+16 <= len(encVerifierHash); i += 16 {
		block.Decrypt(verHash[i:i+16], encVerifierHash[i:i+16])
	}
	want := sha1.Sum(verifier)
	if !bytesEq(want[:], verHash[:20]) {
		return nil, ErrWrongPassword
	}
	// EncryptedPackage: first 8 bytes are the original uncompressed size,
	// followed by AES-ECB encrypted blocks.
	if len(pkg) < 16 {
		return nil, errors.New("standard: package too small")
	}
	origSize := int(binary.LittleEndian.Uint64(pkg[:8]))
	cipherText := pkg[8:]
	plain := make([]byte, len(cipherText))
	for i := 0; i+16 <= len(cipherText); i += 16 {
		block.Decrypt(plain[i:i+16], cipherText[i:i+16])
	}
	if origSize > 0 && origSize <= len(plain) {
		plain = plain[:origSize]
	}
	return plain, nil
}

// deriveStandardKey produces the AES key per §2.3.4.7. password is
// UTF-16LE encoded; key derivation is SHA1 50000-rounds plus the
// "X1 / X2" mix per spec.
func deriveStandardKey(password string, salt []byte, keyBytes int) []byte {
	pwd := utf16LE(password)
	h := sha1.New()
	h.Write(salt)
	h.Write(pwd)
	digest := h.Sum(nil)
	for i := uint32(0); i < 50000; i++ {
		var ib [4]byte
		binary.LittleEndian.PutUint32(ib[:], i)
		h.Reset()
		h.Write(ib[:])
		h.Write(digest)
		digest = h.Sum(nil)
	}
	// Append block 0.
	h.Reset()
	h.Write(digest)
	var block0 [4]byte
	h.Write(block0[:])
	derived := h.Sum(nil)
	// Mix: X1 = derived XOR pad-0x36; X2 = derived XOR pad-0x5c. Final
	// key = SHA1(X1) | SHA1(X2), truncated to keyBytes.
	x1 := make([]byte, 64)
	x2 := make([]byte, 64)
	for i := 0; i < 64; i++ {
		var b byte
		if i < len(derived) {
			b = derived[i]
		}
		x1[i] = b ^ 0x36
		x2[i] = b ^ 0x5C
	}
	h.Reset()
	h.Write(x1)
	d1 := h.Sum(nil)
	h.Reset()
	h.Write(x2)
	d2 := h.Sum(nil)
	full := append(d1, d2...)
	if keyBytes > len(full) {
		keyBytes = len(full)
	}
	return full[:keyBytes]
}

// decryptAgile implements a basic MS-OFFCRYPTO Agile decryption for the
// most common case: AES-256-CBC + SHA-512 keyData. Returns
// ErrAgileUnsupported for parameter combinations we don't handle.
func decryptAgile(info, pkg []byte, password string) ([]byte, error) {
	// Skip 8-byte EncryptionInfo header (version + flags). XML follows.
	xml := string(info[8:])
	// Yank a few fields from the XML — small ad-hoc parser tuned to
	// Microsoft's exact serialization. Avoids pulling encoding/xml for
	// a single document shape.
	keyData := xmlExtractElement(xml, "keyData")
	pwdNode := xmlExtractElement(xml, "p:encryptedKey")
	if keyData == "" || pwdNode == "" {
		return nil, ErrAgileUnsupported
	}
	kdSaltSize := xmlInt(keyData, "saltSize")
	kdBlockSize := xmlInt(keyData, "blockSize")
	kdKeyBits := xmlInt(keyData, "keyBits")
	kdHash := xmlAttr(keyData, "hashAlgorithm")
	kdCipher := xmlAttr(keyData, "cipherAlgorithm")
	kdChaining := xmlAttr(keyData, "cipherChaining")
	if !strings.EqualFold(kdCipher, "AES") {
		return nil, ErrAgileUnsupported
	}
	hashFactory := agileHashFactory(kdHash)
	if hashFactory == nil {
		return nil, ErrAgileUnsupported
	}
	// MS-OFFCRYPTO 2.3.4.13: ChainingModeCBC / ChainingModeCFB.
	// CBC is overwhelmingly common; CFB ships in some EDR-protected
	// docs and a few translation tools. We pick the decrypt routine
	// at run time and use it for both the verifier blocks and the
	// segmented package payload.
	var aesDecrypt func(key, iv, ct []byte) ([]byte, error)
	switch {
	case strings.EqualFold(kdChaining, "ChainingModeCBC"):
		aesDecrypt = aesCBCDecrypt
	case strings.EqualFold(kdChaining, "ChainingModeCFB"):
		aesDecrypt = aesCFBDecrypt
	default:
		return nil, ErrAgileUnsupported
	}
	kdSalt := xmlBase64(keyData, "saltValue")
	pwdSalt := xmlBase64(pwdNode, "saltValue")
	pwdSpin := xmlInt(pwdNode, "spinCount")
	pwdEncKey := xmlBase64(pwdNode, "encryptedKeyValue")
	pwdEncVerInput := xmlBase64(pwdNode, "encryptedVerifierHashInput")
	pwdEncVerValue := xmlBase64(pwdNode, "encryptedVerifierHashValue")
	pwdBlockSize := xmlInt(pwdNode, "blockSize")
	pwdKeyBits := xmlInt(pwdNode, "keyBits")
	_ = kdSaltSize
	_ = kdKeyBits
	if pwdBlockSize <= 0 || pwdBlockSize > len(pwdSalt) {
		pwdBlockSize = 16
	}
	ivLen := kdBlockSize
	if ivLen <= 0 {
		ivLen = aes.BlockSize
	}

	// Verify password with the Verifier blocks (block IDs from spec).
	keyVerHashInput := agileKeyH(hashFactory, password, pwdSalt, pwdSpin, agileBlockVerifierHashInput, pwdKeyBits/8)
	keyVerHashValue := agileKeyH(hashFactory, password, pwdSalt, pwdSpin, agileBlockVerifierHashValue, pwdKeyBits/8)
	verInput, err := aesDecrypt(keyVerHashInput, pwdSalt[:pwdBlockSize], pwdEncVerInput)
	if err != nil {
		return nil, err
	}
	verValue, err := aesDecrypt(keyVerHashValue, pwdSalt[:pwdBlockSize], pwdEncVerValue)
	if err != nil {
		return nil, err
	}
	h := hashFactory()
	h.Write(verInput)
	want := h.Sum(nil)
	if !bytesEq(want[:len(verValue)], verValue) {
		return nil, ErrWrongPassword
	}
	// Derive the package key from EncryptedKeyValue.
	keyEncKey := agileKeyH(hashFactory, password, pwdSalt, pwdSpin, agileBlockEncryptedKeyValue, pwdKeyBits/8)
	pkgKey, err := aesDecrypt(keyEncKey, pwdSalt[:pwdBlockSize], pwdEncKey)
	if err != nil {
		return nil, err
	}
	// Each segment of pkg (first 8 bytes = total size, then 4096-byte
	// segments) is encrypted with IV = H(kdSalt || segIndex)[:blockSize].
	if len(pkg) < 8 {
		return nil, errors.New("agile: package too small")
	}
	origSize := int(binary.LittleEndian.Uint64(pkg[:8]))
	body := pkg[8:]
	const segSz = 4096
	out := make([]byte, 0, origSize)
	for seg := 0; seg*segSz < len(body); seg++ {
		start := seg * segSz
		end := start + segSz
		if end > len(body) {
			end = len(body)
		}
		var idx [4]byte
		binary.LittleEndian.PutUint32(idx[:], uint32(seg))
		hh := hashFactory()
		hh.Write(kdSalt)
		hh.Write(idx[:])
		ivAll := hh.Sum(nil)
		ivCap := ivLen
		if ivCap > len(ivAll) {
			ivCap = len(ivAll)
		}
		iv := ivAll[:ivCap]
		plain, err := aesDecrypt(pkgKey, iv, body[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, plain...)
	}
	if origSize > 0 && origSize <= len(out) {
		out = out[:origSize]
	}
	return out, nil
}

// aesCFBDecrypt mirrors aesCBCDecrypt for MS-OFFCRYPTO ChainingModeCFB.
// The chaining variant Word writes uses the full block as feedback
// (CFB-128), so we use crypto/cipher's NewCFBDecrypter. The IV must be
// at least one AES block; we right-pad with zeros when the seg-IV hash
// produced fewer bytes than aes.BlockSize (rare; depends on hash).
func aesCFBDecrypt(key, iv, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ivPadded := iv
	if len(ivPadded) < aes.BlockSize {
		buf := make([]byte, aes.BlockSize)
		copy(buf, ivPadded)
		ivPadded = buf
	} else {
		ivPadded = ivPadded[:aes.BlockSize]
	}
	out := make([]byte, len(ct))
	cipher.NewCFBDecrypter(block, ivPadded).XORKeyStream(out, ct)
	return out, nil
}

// agileBlock IDs from [MS-OFFCRYPTO] 2.3.4.13.
var (
	agileBlockVerifierHashInput = []byte{0xfe, 0xa7, 0xd2, 0x76, 0x3b, 0x4b, 0x9e, 0x79}
	agileBlockVerifierHashValue = []byte{0xd7, 0xaa, 0x0f, 0x6d, 0x30, 0x61, 0x34, 0x4e}
	agileBlockEncryptedKeyValue = []byte{0x14, 0x6e, 0x0b, 0xe7, 0xab, 0xac, 0xd0, 0xd6}
)

// agileKey runs SHA-512 spin+block derivation per spec.
func agileKey(password string, salt []byte, spinCount int, blockKey []byte, keyBytes int) []byte {
	return agileKeyH(func() hash.Hash { return sha512.New() }, password, salt, spinCount, blockKey, keyBytes)
}

// agileKeyH is agileKey parameterized by the hash factory.
func agileKeyH(hashAlg func() hash.Hash, password string, salt []byte, spinCount int, blockKey []byte, keyBytes int) []byte {
	pwd := utf16LE(password)
	h := hashAlg()
	h.Write(salt)
	h.Write(pwd)
	d := h.Sum(nil)
	for i := uint32(0); i < uint32(spinCount); i++ {
		var ib [4]byte
		binary.LittleEndian.PutUint32(ib[:], i)
		h = hashAlg()
		h.Write(ib[:])
		h.Write(d)
		d = h.Sum(nil)
	}
	h = hashAlg()
	h.Write(d)
	h.Write(blockKey)
	d = h.Sum(nil)
	if keyBytes > len(d) {
		keyBytes = len(d)
	}
	return d[:keyBytes]
}

// agileHashFactory returns a hash.Hash factory for the named Agile hash
// algorithm. Supported: SHA1/SHA256/SHA384/SHA512 (case-insensitive,
// with or without the dash). Unknown → nil.
func agileHashFactory(name string) func() hash.Hash {
	canon := strings.ToUpper(strings.ReplaceAll(name, "-", ""))
	switch canon {
	case "SHA1":
		return func() hash.Hash { return sha1.New() }
	case "SHA256":
		return func() hash.Hash { return sha256.New() }
	case "SHA384":
		return func() hash.Hash { return sha512.New384() }
	case "SHA512":
		return func() hash.Hash { return sha512.New() }
	}
	return nil
}

func aesCBCDecrypt(key, iv, ct []byte) ([]byte, error) {
	if len(ct)%aes.BlockSize != 0 {
		// Pad with zeros to a block boundary (Word writes padded
		// blocks; the trailing zeros are harmless for our verifier
		// comparisons).
		n := aes.BlockSize - (len(ct) % aes.BlockSize)
		ct = append(append([]byte{}, ct...), make([]byte, n)...)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ct))
	mode := cipher.NewCBCDecrypter(block, iv[:aes.BlockSize])
	mode.CryptBlocks(out, ct)
	return out, nil
}

func utf16LE(s string) []byte {
	us := utf16.Encode([]rune(s))
	buf := make([]byte, len(us)*2)
	for i, c := range us {
		buf[i*2] = byte(c)
		buf[i*2+1] = byte(c >> 8)
	}
	return buf
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// xmlExtractElement returns the substring between <name ...> and
// </name>, including the open tag's attributes for downstream
// extraction. Returns "" when name isn't present.
func xmlExtractElement(s, name string) string {
	open := "<" + name
	i := strings.Index(s, open)
	if i < 0 {
		// Try without the "p:" namespace prefix Word emits.
		if strings.HasPrefix(name, "p:") {
			return xmlExtractElement(s, name[2:])
		}
		return ""
	}
	rest := s[i:]
	end := strings.Index(rest, "</")
	if end < 0 {
		// Self-closing or short form: take everything until the next ">".
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			return ""
		}
		return rest[:gt+1]
	}
	return rest[:end]
}

func xmlAttr(s, name string) string {
	needle := name + "=\""
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	rest := s[i+len(needle):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func xmlInt(s, name string) int {
	v := xmlAttr(s, name)
	if v == "" {
		return 0
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func xmlBase64(s, name string) []byte {
	v := xmlAttr(s, name)
	if v == "" {
		return nil
	}
	return decodeBase64(v)
}

// decodeBase64 is a minimal stdlib-free base64 decoder.
func decodeBase64(s string) []byte {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var rev [256]int8
	for i := range rev {
		rev[i] = -1
	}
	for i, c := range alpha {
		rev[byte(c)] = int8(i)
	}
	rev['='] = 0
	out := make([]byte, 0, len(s)*3/4)
	var buf [4]int8
	bi := 0
	pad := 0
	for _, c := range s {
		if c == '=' {
			pad++
		}
		if rev[byte(c)] < 0 {
			continue
		}
		buf[bi] = rev[byte(c)]
		bi++
		if bi == 4 {
			out = append(out,
				byte(buf[0])<<2|byte(buf[1])>>4,
				byte(buf[1])<<4|byte(buf[2])>>2,
				byte(buf[2])<<6|byte(buf[3]))
			bi = 0
		}
	}
	if pad > 0 && len(out) >= pad {
		out = out[:len(out)-pad]
	}
	return out
}
