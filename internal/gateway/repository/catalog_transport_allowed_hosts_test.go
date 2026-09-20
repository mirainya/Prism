package repository

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func validTransportAllowedHostsChange() TransportAllowedHostsChange {
	return TransportAllowedHostsChange{
		catalogChangeGuard: catalogChangeGuard{ExpectedActiveReleaseID: 7, ExpectedConfigVersion: 9},
		TransportCode:      "video-main",
		AllowedHosts: []CatalogAllowedHostInput{
			{Protocol: "https", Host: "cdn.example.com", Port: 443},
		},
	}
}

func TestTransportAllowedHostsChangeNormalizesAndValidatesExactHosts(t *testing.T) {
	in := validTransportAllowedHostsChange()
	in.TransportCode = " VIDEO-MAIN "
	in.AllowedHosts[0] = CatalogAllowedHostInput{Protocol: " HTTPS ", Host: " CDN.Example.COM. ", Port: 443}
	in.Normalize()
	if in.TransportCode != "video-main" || in.AllowedHosts[0].Protocol != "https" || in.AllowedHosts[0].Host != "cdn.example.com" {
		t.Fatalf("normalization failed: %#v", in)
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("valid change rejected: %v", err)
	}

	for name, mutate := range map[string]func(*TransportAllowedHostsChange){
		"missing release":        func(value *TransportAllowedHostsChange) { value.ExpectedActiveReleaseID = 0 },
		"missing config version": func(value *TransportAllowedHostsChange) { value.ExpectedConfigVersion = 0 },
		"missing transport":      func(value *TransportAllowedHostsChange) { value.TransportCode = "" },
		"wildcard":               func(value *TransportAllowedHostsChange) { value.AllowedHosts[0].Host = "*.example.com" },
		"unrestricted":           func(value *TransportAllowedHostsChange) { value.AllowedHosts[0].Host = "0.0.0.0/0" },
		"private address":        func(value *TransportAllowedHostsChange) { value.AllowedHosts[0].Host = "10.0.0.1" },
		"unsupported protocol":   func(value *TransportAllowedHostsChange) { value.AllowedHosts[0].Protocol = "ftp" },
		"missing port":           func(value *TransportAllowedHostsChange) { value.AllowedHosts[0].Port = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validTransportAllowedHostsChange()
			mutate(&candidate)
			candidate.Normalize()
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err=%v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNormalizeCatalogAllowedHostsIncludesBaseAndRejectsProtocolCollision(t *testing.T) {
	hosts, err := normalizeCatalogAllowedHosts("https://API.example.com/v1", []CatalogAllowedHostInput{
		{Protocol: "HTTPS", Host: "api.example.com.", Port: 443},
		{Protocol: "https", Host: "cdn.example.com", Port: 443},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 || hosts[0].Host != "api.example.com" || hosts[1].Host != "cdn.example.com" {
		t.Fatalf("hosts=%#v", hosts)
	}
	_, err = normalizeCatalogAllowedHosts("https://api.example.com", []CatalogAllowedHostInput{
		{Protocol: "http", Host: "api.example.com", Port: 443},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("protocol collision err=%v, want ErrInvalidInput", err)
	}
}

func TestListCatalogTransportAllowedHostsReturnsTransportContext(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ct.transport_code,ct.base_url,r.config_version FROM gw_channel_transports ct JOIN gw_catalog_releases r ON r.id=ct.release_id WHERE ct.release_id=? AND ct.id=?`)).
		WithArgs(uint64(7), uint64(31)).
		WillReturnRows(sqlmock.NewRows([]string{"transport_code", "base_url", "config_version"}).AddRow("video-main", "https://api.example.com", 9))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT protocol,host_pattern,port FROM gw_transport_allowed_hosts WHERE release_id=? AND channel_transport_id=? ORDER BY protocol,host_pattern,port`)).
		WithArgs(uint64(7), uint64(31)).
		WillReturnRows(sqlmock.NewRows([]string{"protocol", "host_pattern", "port"}).
			AddRow("https", "api.example.com", 443).
			AddRow("https", "cdn.example.com", 443))

	result, err := store.ListCatalogTransportAllowedHosts(context.Background(), 7, 31)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReleaseID != 7 || result.TransportID != 31 || result.TransportCode != "video-main" || result.ConfigVersion != 9 || len(result.AllowedHosts) != 2 {
		t.Fatalf("result=%+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChangeTransportAllowedHostsReplacesSetAndAdvancesCatalog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	expectTransportAllowedHostsActiveLock(mock, 7, 9)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,base_url FROM gw_channel_transports WHERE release_id=? AND transport_code=? FOR UPDATE`)).
		WithArgs(uint64(7), "video-main").
		WillReturnRows(sqlmock.NewRows([]string{"id", "base_url"}).AddRow(31, "https://api.example.com/v1"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT protocol,host_pattern,port FROM gw_transport_allowed_hosts WHERE release_id=? AND channel_transport_id=? ORDER BY protocol,host_pattern,port`)).
		WithArgs(uint64(7), uint64(31)).
		WillReturnRows(sqlmock.NewRows([]string{"protocol", "host_pattern", "port"}).AddRow("https", "api.example.com", 443))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM gw_transport_allowed_hosts WHERE release_id=? AND channel_transport_id=?`)).
		WithArgs(uint64(7), uint64(31)).WillReturnResult(sqlmock.NewResult(0, 1))
	for _, host := range []string{"api.example.com", "cdn.example.com"} {
		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO gw_transport_allowed_hosts(release_id,channel_transport_id,protocol,host_pattern,port,created_at) VALUES (?,?,?,?,?,?)`)).
			WithArgs(uint64(7), uint64(31), "https", host, uint16(443), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}
	for _, query := range catalogDigestQueries {
		mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"empty"}))
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM gw_catalog_releases WHERE content_hash=? AND id<>?`)).
		WithArgs(sqlmock.AnyArg(), uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE gw_catalog_readiness SET content_hash=?,heartbeat_at=? WHERE release_id=?`)).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE gw_catalog_releases SET config_version=?,content_hash=?,updated_at=? WHERE id=? AND status='published' AND config_version=?`)).
		WithArgs(uint64(10), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7), uint64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO audit_events(actor_type,actor_user_id,action,resource_type,resource_id,outcome,http_status,metadata,created_at) VALUES ('user',?,?,?,?,'success',200,?,?)`)).
		WithArgs(uint64(42), "catalog_change.transport_allowed_hosts", "catalog_release", "7", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := store.ChangeTransportAllowedHosts(context.Background(), tx, validTransportAllowedHostsChange(), 42)
	if err != nil {
		t.Fatalf("change allowed hosts: %v", err)
	}
	if result.ReleaseID != 7 || result.ConfigVersion != 10 || !result.Activated {
		t.Fatalf("result=%+v", result)
	}
	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectTransportAllowedHostsActiveLock(mock sqlmock.Sqlmock, releaseID, version uint64) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`)).
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(releaseID))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT status,config_version,content_hash,semantic_digest FROM gw_catalog_releases WHERE id=? FOR UPDATE`)).
		WithArgs(releaseID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "config_version", "content_hash", "semantic_digest"}).
			AddRow("published", version, strings.Repeat("a", 64), strings.Repeat("b", 64)))
}
