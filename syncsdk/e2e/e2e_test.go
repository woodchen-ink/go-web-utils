package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RFC 7748 §6.1 test vector: Alice's private and public key.
func TestUserKeyRFC7748Vector(t *testing.T) {
	priv := mustHex(t, "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
	k, err := UserKeyFromBytes(priv)
	if err != nil {
		t.Fatal(err)
	}
	want := mustHex(t, "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	pub := k.Public()
	if !bytes.Equal(pub[:], want) {
		t.Fatalf("public key = %x", pub)
	}
	back, err := ParsePublicKey(pub.String())
	if err != nil || !back.Equal(pub) {
		t.Fatalf("public key round trip failed: %v", err)
	}
}

func TestFingerprintVector(t *testing.T) {
	var pk PublicKey
	copy(pk[:], mustHex(t, "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a"))
	got := pk.Fingerprint()
	const want = fingerprintVector
	if got != want {
		t.Fatalf("fingerprint = %q, want %q", got, want)
	}
	if len(strings.Split(got, " ")) != 8 {
		t.Fatalf("fingerprint should have 8 groups: %q", got)
	}
	var other PublicKey
	other[0] = 9
	if other.Fingerprint() == got {
		t.Fatal("different keys share a fingerprint")
	}
}

func TestParsePublicKeyRejects(t *testing.T) {
	for _, s := range []string{"", "AAAA", EncodeBase64(make([]byte, 31)), "+/+/"} {
		if _, err := ParsePublicKey(s); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParsePublicKey(%q) err = %v", s, err)
		}
	}
}

func TestRecoveryCodeFormatAndParse(t *testing.T) {
	c, err := NewRecoveryCode()
	if err != nil {
		t.Fatal(err)
	}
	s := c.String()
	if len(s) != 39 || strings.Count(s, "-") != 7 {
		t.Fatalf("unexpected format %q", s)
	}
	// Lower case, spaces instead of hyphens, O instead of 0 all parse.
	sloppy := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "-", " "), "0", "O"))
	p, err := ParseRecoveryCode(sloppy)
	if err != nil {
		t.Fatalf("parse %q: %v", sloppy, err)
	}
	if p.String() != s {
		t.Fatal("round trip changed the code")
	}
	// A single typo is caught by the checksum.
	b := []byte(s)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	if _, err := ParseRecoveryCode(string(b)); !errors.Is(err, ErrInvalidRecoveryCode) {
		t.Fatalf("typo not detected: %v", err)
	}
	if _, err := ParseRecoveryCode("short"); !errors.Is(err, ErrInvalidRecoveryCode) {
		t.Fatal("short code accepted")
	}
}

func TestRecoveryCodeVector(t *testing.T) {
	c := &RecoveryCode{}
	for i := range c.entropy {
		c.entropy[i] = byte(i)
	}
	if got := c.String(); got != recoveryVector {
		t.Fatalf("recovery code = %q, want %q", got, recoveryVector)
	}
}

func TestEscrowRoundTripAndReset(t *testing.T) {
	user, _ := GenerateUserKey()
	code, _ := NewRecoveryCode()
	esc, err := SealEscrow(user, code)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(esc)
	var back Escrow
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	parsed, _ := ParseRecoveryCode(code.String())
	got, err := OpenEscrow(&back, parsed, user.Public())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Public().Equal(user.Public()) {
		t.Fatal("recovered a different key")
	}

	// Wrong code.
	other, _ := NewRecoveryCode()
	if _, err := OpenEscrow(&back, other, user.Public()); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong code: %v", err)
	}
	// Escrow paired with another user's public key.
	stranger, _ := GenerateUserKey()
	if _, err := OpenEscrow(&back, parsed, stranger.Public()); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong public key: %v", err)
	}
	// Hostile KDF parameters are refused before any work.
	evil := back
	evil.KDF.MemoryKiB = 1 << 30
	if _, err := OpenEscrow(&evil, parsed, user.Public()); !errors.Is(err, ErrMalformed) {
		t.Fatalf("huge memory accepted: %v", err)
	}

	// Reset: a new code and escrow; the old code no longer opens the new escrow.
	code2, _ := NewRecoveryCode()
	esc2, _ := SealEscrow(user, code2)
	if _, err := OpenEscrow(esc2, parsed, user.Public()); !errors.Is(err, ErrDecrypt) {
		t.Fatal("old recovery code opens the reset escrow")
	}
	if _, err := OpenEscrow(esc2, code2, user.Public()); err != nil {
		t.Fatal(err)
	}
}

func newRingFor(t *testing.T, space string, members ...*UserKey) (*Keyring, []*Keyring) {
	t.Helper()
	ring, err := NewKeyring(space)
	if err != nil {
		t.Fatal(err)
	}
	var out []*Keyring
	for _, m := range members {
		r := EmptyKeyring(space)
		grants, err := ring.GrantMember(m.Public())
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range grants {
			if err := r.AddWrapped(g.Version, g.Wrapped, m); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, r)
	}
	return ring, out
}

func TestEnvelopeRoundTripAndAAD(t *testing.T) {
	alice, _ := GenerateUserKey()
	ring, rings := newRingFor(t, "org_1", alice)
	env, err := ring.Seal("shipments", "doc1", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env)
	parsed, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := rings[0].Open("shipments", "doc1", parsed)
	if err != nil || string(pt) != `{"a":1}` {
		t.Fatalf("open: %q %v", pt, err)
	}

	cases := []struct{ space, coll, id string }{
		{"org_1", "shipments", "doc2"},
		{"org_1", "contacts", "doc1"},
		{"org_2", "shipments", "doc1"},
		{"org_1", "shipment", "sdoc1"}, // boundary shift must not collide
	}
	for _, c := range cases {
		r := rings[0]
		if c.space != "org_1" {
			r = ring.Clone()
			r.space = c.space
		}
		if _, err := r.Open(c.coll, c.id, parsed); !errors.Is(err, ErrDecrypt) {
			t.Errorf("moved to %v: err = %v", c, err)
		}
	}

	tampered := *parsed
	tampered.CT = append([]byte(nil), parsed.CT...)
	tampered.CT[0] ^= 1
	if _, err := rings[0].Open("shipments", "doc1", &tampered); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tamper: %v", err)
	}
	wrongKV := *parsed
	wrongKV.KV = 7
	if _, err := rings[0].Open("shipments", "doc1", &wrongKV); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("unknown version: %v", err)
	}
	for _, bad := range []string{`{"v":2,"kv":1,"nonce":"AAAAAAAAAAAAAAAA","ct":"AAAAAAAAAAAAAAAAAAAAAA"}`, `{"v":1,"kv":1,"nonce":"AAAA","ct":"AAAAAAAAAAAAAAAAAAAAAA"}`, `{"v":1,"kv":1,"nonce":"AAAAAAAAAAAAAAAA","ct":"AAAAAAAAAAAAAAAAAAAAAA","x":1}`, `"str"`} {
		if _, err := ParseEnvelope([]byte(bad)); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseEnvelope(%s) err = %v", bad, err)
		}
	}
}

func TestDocAADUnambiguous(t *testing.T) {
	a := DocAAD("ab", "c", "d", 1, 1)
	b := DocAAD("a", "bc", "d", 1, 1)
	if bytes.Equal(a, b) {
		t.Fatal("AAD is ambiguous")
	}
}

func TestWrapRejectsRelabel(t *testing.T) {
	alice, _ := GenerateUserKey()
	ring, _ := NewKeyring("org_1")
	w, _ := ring.Wrap(1, alice.Public())

	if err := EmptyKeyring("org_1").AddWrapped(2, w, alice); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("relabelled version accepted: %v", err)
	}
	if err := EmptyKeyring("org_2").AddWrapped(1, w, alice); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("other space accepted: %v", err)
	}
	bob, _ := GenerateUserKey()
	if err := EmptyKeyring("org_1").AddWrapped(1, w, bob); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong recipient opened: %v", err)
	}
	// A key from a different space keyring with the same id but a different
	// blob key must not be merged.
	impostor, _ := NewKeyring("org_1")
	r := EmptyKeyring("org_1")
	_ = r.AddWrapped(1, w, alice)
	iw, _ := impostor.Rotate()
	w2, _ := impostor.Wrap(iw, alice.Public())
	if err := r.AddWrapped(iw, w2, alice); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("foreign blob key merged: %v", err)
	}
}

func TestRotationExcludesRemovedMember(t *testing.T) {
	alice, _ := GenerateUserKey()
	bob, _ := GenerateUserKey()
	ring, rings := newRingFor(t, "org_1", alice, bob)
	bobRing := rings[1]
	old, _ := ring.Seal("c", "old", []byte("before"))

	next := ring.Clone()
	v, grants, err := next.RotateFor([]PublicKey{alice.Public()})
	if err != nil || v != 2 || len(grants) != 1 {
		t.Fatalf("rotate: v=%d grants=%d err=%v", v, len(grants), err)
	}
	if ring.Current() != 1 {
		t.Fatal("rotating a clone changed the original")
	}
	aliceRing := rings[0]
	if err := aliceRing.AddWrapped(2, grants[0].Wrapped, alice); err != nil {
		t.Fatal(err)
	}
	if aliceRing.Current() != 2 {
		t.Fatal("current did not advance")
	}
	fresh, _ := aliceRing.Seal("c", "new", []byte("after"))
	if fresh.KV != 2 {
		t.Fatalf("new doc uses kv %d", fresh.KV)
	}
	if _, err := bobRing.Open("c", "new", fresh); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("removed member read new doc: %v", err)
	}
	if pt, err := aliceRing.Open("c", "old", old); err != nil || string(pt) != "before" {
		t.Fatal("old document unreadable after rotation")
	}
	// Bob cannot open Alice's grant either.
	if err := bobRing.AddWrapped(2, grants[0].Wrapped, bob); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("bob opened alice's grant: %v", err)
	}
}

func TestKeyringMarshalRoundTrip(t *testing.T) {
	ring, _ := NewKeyring("me")
	_, _ = ring.Rotate()
	data, err := ring.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	back := EmptyKeyring("me")
	if err := back.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if back.Current() != 2 || len(back.Versions()) != 2 {
		t.Fatalf("versions = %v", back.Versions())
	}
	env, _ := ring.Seal("c", "d", []byte("x"))
	if _, err := back.Open("c", "d", env); err != nil {
		t.Fatal(err)
	}
	if err := EmptyKeyring("other").UnmarshalBinary(data); !errors.Is(err, ErrKeyMismatch) {
		t.Fatal("keyring loaded into another space")
	}
	if err := EmptyKeyring("me").UnmarshalBinary(data[:len(data)-1]); !errors.Is(err, ErrMalformed) {
		t.Fatal("truncated keyring accepted")
	}
}

func encryptBlob(t *testing.T, ring *Keyring, content []byte, chunk int) (string, []byte) {
	t.Helper()
	id, n, err := ring.BlobID(bytes.NewReader(content))
	if err != nil || n != int64(len(content)) {
		t.Fatal(err)
	}
	r, err := ring.NewBlobEncrypter(id, bytes.NewReader(content), chunk)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(ct)) != EncryptedBlobSize(int64(len(content)), chunk) {
		t.Fatalf("size %d != predicted %d", len(ct), EncryptedBlobSize(int64(len(content)), chunk))
	}
	return id, ct
}

func decryptBlob(ring *Keyring, id string, ct []byte) ([]byte, error) {
	r, err := ring.NewBlobDecrypter(id, bytes.NewReader(ct))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func TestBlobRoundTripSizes(t *testing.T) {
	ring, _ := NewKeyring("org_1")
	const chunk = 1024
	for _, size := range []int{0, 1, chunk - 1, chunk, chunk + 1, 3 * chunk, 3*chunk + 17} {
		content := make([]byte, size)
		_, _ = rand.Read(content)
		id, ct := encryptBlob(t, ring, content, chunk)
		if len(id) != 64 {
			t.Fatalf("blob id length %d", len(id))
		}
		got, err := decryptBlob(ring, id, ct)
		if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("size %d: err=%v", size, err)
		}
	}
	// Same content, same id; another space, another id.
	a, _, _ := ring.BlobID(strings.NewReader("hello"))
	b, _, _ := ring.BlobID(strings.NewReader("hello"))
	other, _ := NewKeyring("org_2")
	c, _, _ := other.BlobID(strings.NewReader("hello"))
	if a != b || a == c {
		t.Fatal("blob ids not deterministic per space")
	}
	// Blob ids survive rotation (blob-ID key does not rotate) and old blobs
	// stay readable.
	content := []byte(strings.Repeat("x", 5000))
	id, ct := encryptBlob(t, ring, content, chunk)
	_, _ = ring.Rotate()
	id2, _, _ := ring.BlobID(bytes.NewReader(content))
	if id != id2 {
		t.Fatal("blob id changed after rotation")
	}
	if got, err := decryptBlob(ring, id, ct); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("old blob unreadable: %v", err)
	}
}

func TestBlobTamperDetection(t *testing.T) {
	ring, _ := NewKeyring("org_1")
	const chunk = 1024
	content := make([]byte, 3*chunk+10)
	_, _ = rand.Read(content)
	id, ct := encryptBlob(t, ring, content, chunk)
	full := blobChunkOverhead + chunk

	check := func(name string, data []byte, blobID string, want ...error) {
		t.Helper()
		_, err := decryptBlob(ring, blobID, data)
		for _, w := range want {
			if errors.Is(err, w) {
				return
			}
		}
		t.Errorf("%s: err = %v, want one of %v", name, err, want)
	}

	flip := append([]byte(nil), ct...)
	flip[blobHeaderSize+20] ^= 1
	check("bit flip", flip, id, ErrDecrypt)

	check("truncated at chunk boundary", ct[:blobHeaderSize+2*full], id, ErrDecrypt)
	check("truncated mid chunk", ct[:blobHeaderSize+full+100], id, ErrDecrypt)

	swapped := append([]byte(nil), ct[:blobHeaderSize]...)
	swapped = append(swapped, ct[blobHeaderSize+full:blobHeaderSize+2*full]...)
	swapped = append(swapped, ct[blobHeaderSize:blobHeaderSize+full]...)
	swapped = append(swapped, ct[blobHeaderSize+2*full:]...)
	check("reordered chunks", swapped, id, ErrDecrypt)

	appended := append(append([]byte(nil), ct...), ct[blobHeaderSize:blobHeaderSize+full]...)
	check("appended chunk", appended, id, ErrDecrypt)

	other, _, _ := ring.BlobID(strings.NewReader("other"))
	check("moved to another id", ct, other, ErrDecrypt)

	hdr := append([]byte(nil), ct...)
	hdr[8] = 9 // kv := 9
	check("unknown key version", hdr, id, ErrUnknownKeyVersion)

	foreign, _ := NewKeyring("org_1")
	if _, err := decryptBlob(foreign, id, ct); !errors.Is(err, ErrDecrypt) {
		t.Errorf("foreign key: %v", err)
	}

	// Content uploaded under a wrong id by a member who holds the space key:
	// chunks authenticate (AAD uses the claimed id) but the HMAC check fails.
	liar, err := ring.NewBlobEncrypter(other, bytes.NewReader(content), chunk)
	if err != nil {
		t.Fatal(err)
	}
	lied, _ := io.ReadAll(liar)
	check("content under wrong id", lied, other, ErrBlobMismatch)
}

func TestDeviceApprovalProtocol(t *testing.T) {
	user, _ := GenerateUserKey()
	n, err := NewDeviceRequester()
	if err != nil {
		t.Fatal(err)
	}
	commitment := n.Commitment()
	tempPub := n.TempPublic()

	na, _ := NewDeviceNonce()
	const reqID = "dev_123"
	codeN, err := n.Code(reqID, user.Public(), na)
	if err != nil {
		t.Fatal(err)
	}
	nn := n.Nonce()
	if !VerifyDeviceCommitment(tempPub, commitment, nn) {
		t.Fatal("commitment does not verify")
	}
	codeA, _ := DeviceSAS(reqID, tempPub, user.Public(), nn, na)
	if codeA != codeN || len(codeA) != 6 {
		t.Fatalf("codes differ: %s vs %s", codeA, codeN)
	}

	// A substituted temp key changes the approver's code and fails the commitment.
	mitm, _ := NewDeviceRequester()
	if VerifyDeviceCommitment(mitm.TempPublic(), commitment, nn) {
		t.Fatal("commitment verified for another key")
	}
	if c, _ := DeviceSAS(reqID, mitm.TempPublic(), user.Public(), nn, na); c == codeN {
		t.Fatal("substituted key produced the same code")
	}

	sealed, err := SealUserKeyForDevice(reqID, user, tempPub)
	if err != nil {
		t.Fatal(err)
	}
	got, err := n.OpenUserKey(reqID, sealed, user.Public())
	if err != nil || !got.Public().Equal(user.Public()) {
		t.Fatalf("open: %v", err)
	}
	if _, err := n.OpenUserKey("dev_other", sealed, user.Public()); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("request id not bound: %v", err)
	}
	stranger, _ := GenerateUserKey()
	if _, err := n.OpenUserKey(reqID, sealed, stranger.Public()); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("user public key not checked: %v", err)
	}
	if _, err := mitm.OpenUserKey(reqID, sealed, user.Public()); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other device opened the seal: %v", err)
	}
}

func TestDeviceSASVector(t *testing.T) {
	var temp, user PublicKey
	for i := range temp {
		temp[i] = byte(i)
		user[i] = byte(255 - i)
	}
	nn := bytes.Repeat([]byte{1}, DeviceNonceSize)
	na := bytes.Repeat([]byte{2}, DeviceNonceSize)
	got, err := DeviceSAS("req", temp, user, nn, na)
	if err != nil {
		t.Fatal(err)
	}
	if got != sasVector {
		t.Fatalf("SAS = %s, want %s", got, sasVector)
	}
}

func TestCrockfordRoundTrip(t *testing.T) {
	for n := 5; n <= 30; n += 5 {
		b := make([]byte, n)
		_, _ = rand.Read(b)
		s := encodeCrockford(b)
		got, ok := decodeCrockford(s, n)
		if !ok || !bytes.Equal(got, b) {
			t.Fatalf("n=%d round trip failed", n)
		}
	}
}
