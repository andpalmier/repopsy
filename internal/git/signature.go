package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// readSignatures fills in the signature details carried by each commit object.
//
// git's %G? needs a verifier for every kind of signature: without an
// allowed-signers file it reports an SSH signature as "N", the same verdict as
// an unsigned commit. Reading the gpgsig header directly tells the two apart and
// recovers the issuing key, neither of which needs the key itself.
func (r *Repository) readSignatures(ctx context.Context, commits []Commit) error {
	if len(commits) == 0 {
		return nil
	}
	ids := make([]string, len(commits))
	for i, c := range commits {
		ids[i] = c.Hash
	}
	return r.catFileBatch(ctx, ids, func(i int, content []byte) error {
		if armor := signatureHeader(content); armor != "" {
			details := ParseSignature(armor)
			details.Status, details.Signer = commits[i].Signature.Status, commits[i].Signature.Signer
			details.Key, details.Fingerprint = commits[i].Signature.Key, commits[i].Signature.Fingerprint
			details.Trust = commits[i].Signature.Trust
			details.NoAllowedSigners = details.Type == SigSSH && !r.sshAllowedSigners
			commits[i].Signature = details
		}
		return nil
	})
}

// signatureHeader returns the armored signature in a raw commit object's
// gpgsig (or gpgsig-sha256) header, or "" if there is none. Continuation lines
// of a header start with a single space.
func signatureHeader(object []byte) string {
	headers, _, _ := bytes.Cut(object, []byte("\n\n"))
	var sig []string
	inSig := false
	for line := range strings.SplitSeq(string(headers), "\n") {
		switch {
		case inSig && strings.HasPrefix(line, " "):
			sig = append(sig, line[1:])
		case strings.HasPrefix(line, "gpgsig ") || strings.HasPrefix(line, "gpgsig-sha256 "):
			if sig != nil {
				// A second signature (sha1 and sha256 both present) adds
				// nothing the first does not say about the issuer.
				return strings.Join(sig, "\n")
			}
			_, value, _ := strings.Cut(line, " ")
			sig = []string{value}
			inSig = true
		default:
			inSig = false
		}
	}
	return strings.Join(sig, "\n")
}

// ParseSignature reads what an armored signature says about itself: its type,
// the key it claims to be made with and, for OpenPGP, any signer identity it
// states. Nothing here verifies anything.
func ParseSignature(armor string) Signature {
	switch {
	case strings.Contains(armor, "-----BEGIN PGP SIGNATURE-----"):
		s := Signature{Type: SigGPG}
		if body, ok := dearmor(armor); ok {
			parsePGPSignature(body, &s)
		}
		return s
	case strings.Contains(armor, "-----BEGIN SSH SIGNATURE-----"):
		s := Signature{Type: SigSSH}
		if body, ok := dearmor(armor); ok {
			parseSSHSignature(body, &s)
		}
		return s
	case strings.Contains(armor, "-----BEGIN SIGNED MESSAGE-----"):
		return Signature{Type: SigX509}
	case strings.TrimSpace(armor) == "":
		return Signature{}
	default:
		return Signature{Type: SigUnknown}
	}
}

// dearmor decodes the base64 body between an armor's BEGIN and END lines,
// skipping armor headers and the OpenPGP checksum line.
func dearmor(armor string) ([]byte, bool) {
	var b64 strings.Builder
	inBody := false
	for line := range strings.SplitSeq(armor, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "-----BEGIN"):
			inBody = true
		case strings.HasPrefix(line, "-----END"):
			inBody = false
		case !inBody, strings.Contains(line, ": "), strings.HasPrefix(line, "="):
			// Armor headers ("Comment: ...") and the CRC line ("=abcd").
		default:
			b64.WriteString(line)
		}
	}
	body, err := base64.StdEncoding.DecodeString(b64.String())
	return body, err == nil
}

// pgpAlgorithms names OpenPGP public key algorithms (RFC 9580, 9.1).
var pgpAlgorithms = map[byte]string{
	1: "RSA", 2: "RSA", 3: "RSA", 16: "Elgamal", 17: "DSA", 18: "ECDH",
	19: "ECDSA", 22: "EdDSA", 25: "X25519", 27: "Ed25519", 28: "Ed448",
}

// parsePGPSignature reads the issuer of an OpenPGP signature packet. Anything
// malformed simply leaves the fields empty: the signature is still reported as
// present, which is the part that must never be lost.
func parsePGPSignature(data []byte, s *Signature) {
	body, ok := pgpPacketBody(data)
	if !ok || len(body) < 1 {
		return
	}
	switch version := body[0]; version {
	case 3:
		// v3: version, 5, type, 4-byte time, 8-byte key ID, algorithm, hash.
		if len(body) >= 16 {
			s.IssuerKeyID = strings.ToUpper(hex.EncodeToString(body[7:15]))
			s.Algorithm = pgpAlgorithms[body[15]]
		}
	case 4, 5, 6:
		if len(body) < 4 {
			return
		}
		s.Algorithm = pgpAlgorithms[body[2]]
		rest := body[4:]
		// The hashed area is covered by the signature, the unhashed one is not;
		// both are read, since GnuPG puts the issuer key ID in the latter.
		for range 2 {
			var n int
			if version == 6 {
				if len(rest) < 4 {
					return
				}
				n, rest = int(binary.BigEndian.Uint32(rest)), rest[4:]
			} else {
				if len(rest) < 2 {
					return
				}
				n, rest = int(binary.BigEndian.Uint16(rest)), rest[2:]
			}
			if n > len(rest) {
				return
			}
			parsePGPSubpackets(rest[:n], s)
			rest = rest[n:]
		}
	}
}

// pgpPacketBody returns the body of the first packet in data, which for a
// detached signature is the signature packet.
func pgpPacketBody(data []byte) ([]byte, bool) {
	if len(data) < 2 || data[0]&0x80 == 0 {
		return nil, false
	}
	var length, offset int
	if data[0]&0x40 != 0 {
		// New format: the length follows the tag byte.
		switch first := int(data[1]); {
		case first < 192:
			length, offset = first, 2
		case first < 224 && len(data) >= 3:
			length, offset = (first-192)<<8+int(data[2])+192, 3
		case first == 255 && len(data) >= 6:
			length, offset = int(binary.BigEndian.Uint32(data[2:6])), 6
		default:
			return nil, false
		}
	} else {
		// Old format: the low two bits say how many length bytes follow.
		switch data[0] & 3 {
		case 0:
			length, offset = int(data[1]), 2
		case 1:
			if len(data) < 3 {
				return nil, false
			}
			length, offset = int(binary.BigEndian.Uint16(data[1:3])), 3
		case 2:
			if len(data) < 5 {
				return nil, false
			}
			length, offset = int(binary.BigEndian.Uint32(data[1:5])), 5
		default:
			length, offset = len(data)-1, 1
		}
	}
	if offset+length > len(data) || length < 0 {
		return nil, false
	}
	return data[offset : offset+length], true
}

// parsePGPSubpackets reads the issuer and signer subpackets of a signature.
func parsePGPSubpackets(area []byte, s *Signature) {
	for len(area) > 0 {
		var n, hdr int
		switch first := int(area[0]); {
		case first < 192:
			n, hdr = first, 1
		case first < 255 && len(area) >= 2:
			n, hdr = (first-192)<<8+int(area[1])+192, 2
		case first == 255 && len(area) >= 5:
			n, hdr = int(binary.BigEndian.Uint32(area[1:5])), 5
		default:
			return
		}
		if n < 1 || hdr+n > len(area) {
			return
		}
		kind, value := area[hdr]&0x7f, area[hdr+1:hdr+n]
		area = area[hdr+n:]

		switch kind {
		case 16: // issuer key ID
			if len(value) == 8 && s.IssuerKeyID == "" {
				s.IssuerKeyID = strings.ToUpper(hex.EncodeToString(value))
			}
		case 33: // issuer fingerprint: a version byte, then the fingerprint
			if len(value) > 1 {
				s.IssuerFingerprint = strings.ToUpper(hex.EncodeToString(value[1:]))
				if s.IssuerKeyID == "" && len(value) == 21 {
					// A v4 key ID is the low 64 bits of its fingerprint.
					s.IssuerKeyID = s.IssuerFingerprint[len(s.IssuerFingerprint)-16:]
				}
			}
		case 28: // signer's user ID
			s.ClaimedSigner = string(value)
		}
	}
}

// parseSSHSignature reads the public key from an SSHSIG blob and derives the
// fingerprint ssh-keygen would print for it.
//
//	"SSHSIG" uint32(version) string(publickey) string(namespace) ...
func parseSSHSignature(data []byte, s *Signature) {
	rest, ok := bytes.CutPrefix(data, []byte("SSHSIG"))
	if !ok || len(rest) < 4 {
		return
	}
	key, _, ok := sshString(rest[4:])
	if !ok {
		return
	}
	sum := sha256.Sum256(key)
	s.IssuerFingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	if algorithm, _, ok := sshString(key); ok {
		s.Algorithm = string(algorithm)
	}
}

// sshString reads one length-prefixed string of the SSH wire format.
func sshString(b []byte) (value, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

// Describe renders what is known about the key a signature claims, or "" when
// nothing is.
func (s Signature) Describe() string {
	var parts []string
	if s.IssuerKeyID != "" {
		parts = append(parts, "key ID "+s.IssuerKeyID)
	}
	if s.IssuerFingerprint != "" {
		parts = append(parts, "fingerprint "+s.IssuerFingerprint)
	}
	desc := strings.Join(parts, ", ")
	if s.Algorithm != "" && desc != "" {
		desc += fmt.Sprintf(" (%s)", s.Algorithm)
	}
	return desc
}
