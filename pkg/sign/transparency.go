// Copyright 2024 The Sigstore Authors.
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

package sign

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/rekor/pkg/client"
	"github.com/sigstore/rekor/pkg/generated/client/entries"
	"github.com/sigstore/rekor/pkg/generated/models"
	"github.com/sigstore/rekor/pkg/pki"
	"github.com/sigstore/rekor/pkg/tle"
	"github.com/sigstore/rekor/pkg/types"
	"github.com/sigstore/rekor/pkg/types/dsse"
	"github.com/sigstore/rekor/pkg/types/hashedrekord"
	rekorUtil "github.com/sigstore/rekor/pkg/util"

	// To initialize rekor types
	_ "github.com/sigstore/rekor/pkg/types/dsse/v0.0.1"
	_ "github.com/sigstore/rekor/pkg/types/hashedrekord/v0.0.1"

	"github.com/sigstore/sigstore-go/pkg/util"
)

type RekorClient interface {
	CreateLogEntry(params *entries.CreateLogEntryParams, opts ...entries.ClientOption) (*entries.CreateLogEntryCreated, error)
}

type Transparency interface {
	GetTransparencyLogEntry(context.Context, []byte, *protobundle.Bundle) error
}

type Rekor struct {
	options *RekorOptions
}

type RekorOptions struct {
	// URL of Fulcio instance
	BaseURL string
	// Optional timeout for network requests (default 30s; use negative value for no timeout)
	Timeout time.Duration
	// Optional number of times to retry
	Retries uint
	// Optional client (for dependency injection)
	Client RekorClient
}

func NewRekor(opts *RekorOptions) *Rekor {
	return &Rekor{options: opts}
}

func (r *Rekor) GetTransparencyLogEntry(ctx context.Context, pubKeyPEM []byte, b *protobundle.Bundle) error {
	artifactProperties := types.ArtifactProperties{
		PublicKeyBytes: [][]byte{pubKeyPEM},
	}

	dsseEnvelope := b.GetDsseEnvelope()
	messageSignature := b.GetMessageSignature()
	verificationMaterial := b.GetVerificationMaterial()
	bundleCertificate := verificationMaterial.GetCertificate()

	var proposedEntry models.ProposedEntry

	switch {
	case dsseEnvelope != nil:
		dsseType := dsse.New()

		artifactBytes, err := json.Marshal(dsseEnvelope)
		if err != nil {
			return err
		}

		artifactProperties.ArtifactBytes = artifactBytes

		proposedEntry, err = dsseType.CreateProposedEntry(ctx, "", artifactProperties)
		if err != nil {
			return err
		}
	case messageSignature != nil:
		hashedrekordType := hashedrekord.New()

		if bundleCertificate == nil {
			return errors.New("hashedrekord requires X.509 certificate")
		}

		hexDigest := hex.EncodeToString(messageSignature.MessageDigest.Digest)

		artifactProperties.PKIFormat = string(pki.X509)
		artifactProperties.SignatureBytes = messageSignature.Signature
		artifactProperties.ArtifactHash = rekorUtil.PrefixSHA(hexDigest)

		var err error
		proposedEntry, err = hashedrekordType.CreateProposedEntry(ctx, "", artifactProperties)
		if err != nil {
			return err
		}
	default:
		return errors.New("unable to find signature in bundle")
	}

	params := entries.NewCreateLogEntryParams()
	if r.options.Timeout >= 0 {
		if r.options.Timeout == 0 {
			r.options.Timeout = 30 * time.Second
		}
		params.SetTimeout(r.options.Timeout)
	}
	params.SetProposedEntry(proposedEntry)
	params.SetContext(ctx)

	if r.options.Client == nil {
		client, err := client.GetRekorClient(r.options.BaseURL, client.WithUserAgent(util.ConstructUserAgent()), client.WithRetryCount(r.options.Retries))
		if err != nil {
			return err
		}
		r.options.Client = client.Entries
	}

	resp, err := r.options.Client.CreateLogEntry(params)
	if err != nil {
		return err
	}

	entry := resp.Payload[resp.ETag]
	tlogEntry, err := tle.GenerateTransparencyLogEntry(entry)
	if err != nil {
		return err
	}

	if b.VerificationMaterial.TlogEntries == nil {
		b.VerificationMaterial.TlogEntries = []*protorekor.TransparencyLogEntry{}
	}

	b.VerificationMaterial.TlogEntries = append(b.VerificationMaterial.TlogEntries, tlogEntry)

	return nil
}
