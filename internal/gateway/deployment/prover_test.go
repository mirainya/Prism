package deployment

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestProbeKeyMaterialExercisesMACWrapAndEncryption(t *testing.T) {
	kek := bytes.Repeat([]byte{0x41}, security.KeySize)
	hmacKey := bytes.Repeat([]byte{0x42}, security.KeySize)
	if err := probeKeyMaterial(kek, hmacKey, 7); err != nil {
		t.Fatal(err)
	}
	if err := probeKeyMaterial(kek[:security.KeySize-1], hmacKey, 7); err == nil {
		t.Fatal("invalid KEK was accepted")
	}
}

func TestRequiredOperationsDistinguishesWritableAndReadableVersions(t *testing.T) {
	if got := requiredOperations("current"); len(got) != 5 {
		t.Fatalf("current operations = %v", got)
	}
	if got := requiredOperations("readable"); len(got) != 3 {
		t.Fatalf("readable operations = %v", got)
	}
}

func TestProveDerivesCurrentMemberCatalogAndCryptoProofs(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, security.KeySize))
	for _, name := range []string{"PRISM_GATEWAY_KEK_B64", "PRISM_GATEWAY_HMAC_B64", "PRISM_GATEWAY_PAYLOAD_KEK_B64", "PRISM_GATEWAY_PAYLOAD_HMAC_B64"} {
		t.Setenv(name, key)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	identity := repository.DeploymentIdentity{InstanceID: "instance-a", Role: "api-worker", AdapterDigest: strings.Repeat("a", 64)}
	prover, err := NewProver(store, identity)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := adapter.DescriptorFor("openai_chat", 1)
	if !ok {
		t.Fatal("missing test adapter")
	}
	semantic, content := adapter.SemanticDigest(), strings.Repeat("b", 64)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status,semantic_digest FROM gw_deployment_generations`).WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"status", "semantic_digest"}).AddRow("preparing", semantic))
	mock.ExpectQuery(`SELECT id FROM gw_deployment_members`).WithArgs(uint64(7), identity.InstanceID, identity.Role).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT status,member_frozen_at FROM gw_deployment_generations`).WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"status", "member_frozen_at"}).AddRow("preparing", nil))
	mock.ExpectQuery(`SELECT id FROM gw_deployment_members`).WithArgs(uint64(7), identity.InstanceID, identity.Role).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO gw_deployment_members`).WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectQuery(`SELECT active_release_id FROM gw_catalog_runtime_state`).WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(nil))
	mock.ExpectQuery(`SELECT id FROM gw_catalog_releases`).WithArgs(semantic).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(13))
	mock.ExpectQuery(`SELECT status,content_hash,semantic_digest FROM gw_catalog_releases`).WithArgs(uint64(13)).WillReturnRows(sqlmock.NewRows([]string{"status", "content_hash", "semantic_digest"}).AddRow("published", content, semantic))
	mock.ExpectQuery(`SELECT DISTINCT a.adapter_code`).WithArgs(uint64(13)).WillReturnRows(sqlmock.NewRows([]string{"adapter_code", "contract_version", "implementation_digest", "minimum_semantic_version"}).AddRow(descriptor.Code, descriptor.Version, descriptor.ImplementationDigest, descriptor.MinimumSemanticVersion))
	mock.ExpectQuery(`SELECT EXISTS \(`).WillReturnRows(sqlmock.NewRows([]string{"required"}).AddRow(true))
	mock.ExpectQuery(`SELECT k.id,k.purpose,v.key_version`).WithArgs(true).WillReturnRows(sqlmock.NewRows([]string{"id", "purpose", "key_version", "status", "provider_key_ref"}).
		AddRow(1, "gateway-credential", 1, "current", "env:PRISM_GATEWAY_KEK_B64").
		AddRow(2, "gateway-payload", 1, "current", "env:PRISM_GATEWAY_PAYLOAD_KEK_B64"))
	mock.ExpectQuery(`SELECT g.status FROM gw_deployment_generations`).WithArgs(uint64(7), uint64(11)).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("preparing"))
	mock.ExpectQuery(`SELECT status,content_hash,semantic_digest FROM gw_catalog_releases`).WithArgs(uint64(13)).WillReturnRows(sqlmock.NewRows([]string{"status", "content_hash", "semantic_digest"}).AddRow("published", content, semantic))
	mock.ExpectExec(`INSERT INTO gw_catalog_readiness`).WillReturnResult(sqlmock.NewResult(1, 1))
	for range 10 {
		mock.ExpectQuery(`SELECT g.status FROM gw_deployment_generations`).WithArgs(uint64(7), uint64(11)).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("preparing"))
		mock.ExpectExec(`INSERT INTO crypto_key_readiness`).WillReturnResult(sqlmock.NewResult(1, 1))
	}
	mock.ExpectCommit()
	proof, err := prover.Prove(context.Background(), 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if proof.GenerationID != 7 || proof.MemberID != 11 || proof.ReleaseID != 13 || proof.Identity != identity {
		t.Fatalf("proof = %+v", proof)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProbeKeyVersionsAllowsPayloadOnlyConfiguration(t *testing.T) {
	t.Setenv("PRISM_GATEWAY_KEK_B64", "")
	t.Setenv("PRISM_GATEWAY_HMAC_B64", "")
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, security.KeySize))
	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", key)
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", key)

	err := probeKeyVersions([]keyVersion{{
		keyringID: 2, purpose: "gateway-payload", version: 1,
		state: "current", providerRef: "env:PRISM_GATEWAY_PAYLOAD_KEK_B64",
	}})
	if err != nil {
		t.Fatal(err)
	}
}
