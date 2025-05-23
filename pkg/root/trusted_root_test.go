// Copyright 2023 The Sigstore Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package root

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"os"
	"strings"
	"testing"
	"time"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/stretchr/testify/assert"
)

const pkixRsa = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA3wqI/TysUiKTgY1bz+wd
JfEOil4MEsRASKGzJddZ6x9hb+rn2UVoJmuxN62XI0TMoMn4mukgfCgY6jgTB58V
+/LaeSA8Wz1p4gOxhk1mcgbF4HyxR+xlRgYfH4iSbXy+Ez/8ZjM2OO68fKr4JZEA
5LXZkhJr32JqH+UiFw/wgSPWA8aV0AfRAXHdekJ48B1ChxJTrOJWSPTnj/E0lfLV
srJKtXDuC8T0vFmVU726tI6fODsEE6VrSahvw1ENUHzI34sbfrmrggwPO4iMAQvq
wu2gn2lx6ajWsh806FItiXN+DuizMnx4KMBI0IJynoQpWOFbstGiV0LygZkQ6soz
vwIDAQAB
-----END PUBLIC KEY-----`

const pkixEd25519 = `-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA9wy4umF4RHQ8UQXo8fzEQNBWE4GsBMkCzQPAfHvkf/s=
-----END PUBLIC KEY-----`

func TestGetSigstoreTrustedRoot(t *testing.T) {
	trustedrootJSON, err := os.ReadFile("../../examples/trusted-root-public-good.json")
	assert.Nil(t, err)

	trustedRoot, err := NewTrustedRootFromJSON(trustedrootJSON)
	assert.Nil(t, err)
	assert.NotNil(t, trustedRoot)
}

type singleKeyVerifier struct {
	BaseTrustedMaterial
	verifier TimeConstrainedVerifier
}

func (f *singleKeyVerifier) PublicKeyVerifier(_ string) (TimeConstrainedVerifier, error) {
	return f.verifier, nil
}

type nonExpiringVerifier struct {
	signature.Verifier
}

func (*nonExpiringVerifier) ValidAtTime(_ time.Time) bool {
	return true
}

func TestNewTrustedRoot(t *testing.T) {
	trustedrootJSON, err := os.ReadFile("../../examples/trusted-root-public-good.json")
	assert.NoError(t, err)

	tr, err := NewTrustedRootFromJSON(trustedrootJSON)
	assert.NoError(t, err)

	tr2, err := NewTrustedRoot(
		TrustedRootMediaType01,
		tr.certificateAuthorities,
		tr.ctLogs,
		tr.timestampingAuthorities,
		tr.rekorLogs,
	)
	assert.NoError(t, err)
	// tr and tr2 are not "fully" equal because of the trustedRoot field
	assert.Equal(t, tr.trustedRoot.MediaType, TrustedRootMediaType01)
	assert.Equal(t, tr.BaseTrustedMaterial, tr2.BaseTrustedMaterial)
	assert.Equal(t, tr.certificateAuthorities, tr2.certificateAuthorities)
	assert.Equal(t, tr.ctLogs, tr2.ctLogs)
	assert.Equal(t, tr.timestampingAuthorities, tr2.timestampingAuthorities)
	assert.Equal(t, tr.rekorLogs, tr2.rekorLogs)
}

func TestTrustedMaterialCollectionECDSA(t *testing.T) {
	trustedrootJSON, err := os.ReadFile("../../examples/trusted-root-public-good.json")
	assert.NoError(t, err)

	trustedRoot, err := NewTrustedRootFromJSON(trustedrootJSON)
	assert.NoError(t, err)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NoError(t, err)

	ecVerifier, err := signature.LoadECDSAVerifier(key.Public().(*ecdsa.PublicKey), crypto.SHA256)
	assert.NoError(t, err)

	verifier := &nonExpiringVerifier{ecVerifier}
	trustedMaterialCollection := TrustedMaterialCollection{trustedRoot, &singleKeyVerifier{verifier: verifier}}

	verifier2, err := trustedMaterialCollection.PublicKeyVerifier("foo")
	assert.NoError(t, err)
	assert.Equal(t, verifier, verifier2)

	// verify that a JSON round trip works
	jsonBytes, err := json.Marshal(trustedRoot)
	assert.NoError(t, err)

	_, err = NewTrustedRootFromJSON(jsonBytes)
	assert.NoError(t, err)
}

func TestTrustedMaterialCollectionED25519(t *testing.T) {
	trustedrootJSON, err := os.ReadFile("../../examples/trusted-root-public-good.json")
	assert.NoError(t, err)

	trustedRootProto, err := NewTrustedRootProtobuf(trustedrootJSON)
	assert.NoError(t, err)
	for _, ctlog := range trustedRootProto.Ctlogs {
		ctlog.PublicKey.KeyDetails = protocommon.PublicKeyDetails_PKIX_ED25519
		derBytes, _ := pem.Decode([]byte(pkixEd25519))
		ctlog.PublicKey.RawBytes = derBytes.Bytes
	}

	for _, tlog := range trustedRootProto.Tlogs {
		tlog.PublicKey.KeyDetails = protocommon.PublicKeyDetails_PKIX_ED25519
		derBytes, _ := pem.Decode([]byte(pkixEd25519))
		tlog.PublicKey.RawBytes = derBytes.Bytes
	}

	trustedRoot, err := NewTrustedRootFromProtobuf(trustedRootProto)
	assert.NoError(t, err)

	for _, tlog := range trustedRoot.rekorLogs {
		assert.Equal(t, tlog.SignatureHashFunc, crypto.SHA512)
	}

	key, _, err := ed25519.GenerateKey(rand.Reader)
	assert.NoError(t, err)

	ecVerifier, err := signature.LoadED25519Verifier(key)
	assert.NoError(t, err)

	verifier := &nonExpiringVerifier{ecVerifier}
	trustedMaterialCollection := TrustedMaterialCollection{trustedRoot, &singleKeyVerifier{verifier: verifier}}

	verifier2, err := trustedMaterialCollection.PublicKeyVerifier("foo")
	assert.NoError(t, err)
	assert.Equal(t, verifier, verifier2)

	// verify that a JSON round trip works
	jsonBytes, err := json.Marshal(trustedRoot)
	assert.NoError(t, err)

	_, err = NewTrustedRootFromJSON(jsonBytes)
	assert.NoError(t, err)
}

func TestTrustedMaterialCollectionRSA(t *testing.T) {
	trustedrootJSON, err := os.ReadFile("../../examples/trusted-root-public-good.json")
	assert.NoError(t, err)

	trustedRootProto, err := NewTrustedRootProtobuf(trustedrootJSON)
	assert.NoError(t, err)
	for _, ctlog := range trustedRootProto.Ctlogs {
		ctlog.PublicKey.KeyDetails = protocommon.PublicKeyDetails_PKIX_RSA_PKCS1V15_2048_SHA256
		derBytes, _ := pem.Decode([]byte(pkixRsa))
		ctlog.PublicKey.RawBytes = derBytes.Bytes
	}

	for _, tlog := range trustedRootProto.Tlogs {
		tlog.PublicKey.KeyDetails = protocommon.PublicKeyDetails_PKIX_RSA_PKCS1V15_2048_SHA256
		derBytes, _ := pem.Decode([]byte(pkixRsa))
		tlog.PublicKey.RawBytes = derBytes.Bytes
	}

	trustedRoot, err := NewTrustedRootFromProtobuf(trustedRootProto)
	assert.NoError(t, err)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	ecVerifier, err := signature.LoadRSAPKCS1v15Verifier(key.Public().(*rsa.PublicKey), crypto.SHA256)
	assert.NoError(t, err)

	verifier := &nonExpiringVerifier{ecVerifier}
	trustedMaterialCollection := TrustedMaterialCollection{trustedRoot, &singleKeyVerifier{verifier: verifier}}

	verifier2, err := trustedMaterialCollection.PublicKeyVerifier("foo")
	assert.NoError(t, err)
	assert.Equal(t, verifier, verifier2)

	// verify that a JSON round trip works
	jsonBytes, err := json.Marshal(trustedRoot)
	assert.NoError(t, err)

	_, err = NewTrustedRootFromJSON(jsonBytes)
	assert.NoError(t, err)
}

func TestFromJSONToJSON(t *testing.T) {
	trustedrootJSON, err := os.ReadFile("../../examples/trusted-root-public-good.json")
	assert.NoError(t, err)

	trustedRoot, err := NewTrustedRootFromJSON(trustedrootJSON)
	assert.NoError(t, err)

	jsonBytes, err := json.Marshal(trustedRoot)
	assert.NoError(t, err)

	// Protobuf JSON serialization intentionally strips second fraction from time, if
	// the fraction is 0. We do the same to the expected result:
	// https://github.com/golang/protobuf/blob/b7697bb698b1c56643249ef6179c7cae1478881d/jsonpb/encode.go#L207
	trJSONTrimmedTime := strings.ReplaceAll(string(trustedrootJSON), ".000Z\"", "Z\"")

	assert.JSONEq(t, trJSONTrimmedTime, string(jsonBytes))
}

func TestValidityPeriods(t *testing.T) {
	trustedrootJSON, err := os.ReadFile("../../examples/trusted-root-public-good.json")
	assert.NoError(t, err)

	trustedRoot, err := NewTrustedRootFromJSON(trustedrootJSON)
	assert.NoError(t, err)

	// confirm that ValidityPeriodEnd.IsZero() is true for services without end validity date
	assert.True(t, trustedRoot.ctLogs["dd3d306ac6c7113263191e1c99673702a24a5eb8de3cadff878a72802f29ee8e"].ValidityPeriodEnd.IsZero())
	assert.True(t, trustedRoot.certificateAuthorities[1].(*FulcioCertificateAuthority).ValidityPeriodEnd.IsZero())
	assert.True(t, trustedRoot.rekorLogs["c0d23d6ad406973f9559f3ba2d1ca01f84147d8ffc5b8445c224f98b9591801d"].ValidityPeriodEnd.IsZero())
	assert.True(t, trustedRoot.timestampingAuthorities[0].(*SigstoreTimestampingAuthority).ValidityPeriodEnd.IsZero())
}
