package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func testDeploymentIdentity() DeploymentIdentity {
	return DeploymentIdentity{InstanceID: "instance-a", Role: "api-worker", AdapterDigest: strings.Repeat("a", 64)}
}

func TestCatalogReadinessUsesOnePublishedReleaseForEveryMember(t *testing.T) {
	for _, test := range []struct {
		name, change string
		ready        bool
	}{
		{"complete", "", true},
		{"draft_release", `UPDATE gw_catalog_releases SET status='draft'`, false},
		{"retired_generation", `UPDATE gw_deployment_generations SET status='retired'`, false},
		{"wrong_semantics", `UPDATE gw_deployment_generations SET semantic_digest='different'`, false},
		{"wrong_content", `UPDATE gw_catalog_readiness SET content_hash='different' WHERE deployment_member_id=11`, false},
		{"another_release", `UPDATE gw_catalog_readiness SET release_id=2 WHERE deployment_member_id=11`, false},
		{"another_generation", `UPDATE gw_catalog_readiness SET deployment_generation_id=2`, false},
		{"missing_adapter_proof", `UPDATE gw_catalog_readiness SET adapter_digest=''`, false},
		{"different_binary", `UPDATE gw_catalog_readiness SET adapter_digest='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' WHERE deployment_member_id=11`, false},
		{"current_instance_absent", `UPDATE gw_deployment_members SET instance_id='instance-b' WHERE id=10`, false},
		{"expired_proof", `UPDATE gw_catalog_readiness SET expires_at='2098-12-31 00:00:00'`, false},
		{"missing_member", `DELETE FROM gw_catalog_readiness WHERE deployment_member_id=11`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := cryptoFixture(t)
			for _, query := range []string{
				`CREATE TABLE gw_deployment_generations(id INTEGER, status TEXT, semantic_digest TEXT)`,
				`CREATE TABLE gw_catalog_releases(id INTEGER, status TEXT, content_hash TEXT, semantic_digest TEXT)`,
				`CREATE TABLE gw_catalog_readiness(deployment_generation_id INTEGER, deployment_member_id INTEGER, release_id INTEGER, status TEXT, expires_at TEXT, content_hash TEXT, semantic_digest TEXT, adapter_digest TEXT)`,
				`INSERT INTO gw_deployment_generations VALUES (1,'preparing','semantic')`,
				`INSERT INTO gw_catalog_releases VALUES (1,'published','content','semantic'),(2,'published','content','semantic')`,
				`INSERT INTO gw_catalog_readiness SELECT deployment_generation_id,id,1,'ready','2099-01-02 00:00:00','content','semantic','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' FROM gw_deployment_members`,
			} {
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if test.change != "" {
				if _, err := db.Exec(test.change); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckCatalogReadiness(context.Background(), db, 1, 1, testDeploymentIdentity())
			if test.ready && err != nil || !test.ready && !errors.Is(err, ErrConflict) {
				t.Fatalf("ready=%v error=%v", test.ready, err)
			}
		})
	}
}
