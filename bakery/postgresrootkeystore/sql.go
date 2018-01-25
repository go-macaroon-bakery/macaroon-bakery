package postgresrootkeystore

import (
	"bytes"
	"database/sql"
	"fmt"
	"text/template"
	"time"

	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/dbrootkeystore"
)

type stmtId int

const (
	findIdStmt stmtId = iota
	findBestRootKeyStmt
	insertKeyStmt
	numStmts
)

var initStatements = `
CREATE TABLE IF NOT EXISTS {{.Table}} (
	id BYTEA PRIMARY KEY NOT NULL,
	rootkey BYTEA,
	created TIMESTAMP WITH TIME ZONE NOT NULL,
	expires TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE OR REPLACE FUNCTION {{.ExpireFunc}}() RETURNS trigger
LANGUAGE plpgsql
AS $$
	BEGIN
		DELETE FROM {{.Table}} WHERE expires < NOW();
		RETURN NEW;
	END;
$$;

CREATE INDEX IF NOT EXISTS {{.CreateIndex}} ON {{.Table}} (created);

CREATE INDEX IF NOT EXISTS {{.ExpireIndex}} ON {{.Table}} (expires);

DROP TRIGGER IF EXISTS {{.ExpireTrigger}} ON {{.Table}};

CREATE TRIGGER {{.ExpireTrigger}}
   BEFORE INSERT ON {{.Table}}
   EXECUTE PROCEDURE {{.ExpireFunc}}();
`

type templateParams struct {
	Table         string
	ExpireFunc    string
	CreateIndex   string
	ExpireIndex   string
	ExpireTrigger string
}

func (s *RootKeys) initDB() error {
	s.initDBOnce.Do(func() {
		s.initDBErr = s._initDB()
	})
	if s.initDBErr != nil {
		return errgo.Notef(s.initDBErr, "cannot initialize database")
	}
	return nil
}

func (s *RootKeys) _initDB() error {
	p := &templateParams{
		Table:         s.table,
		ExpireFunc:    s.table + "_expire_func",
		CreateIndex:   s.table + "_index_create",
		ExpireIndex:   s.table + "_index_expire",
		ExpireTrigger: s.table + "_trigger",
	}
	if _, err := s.db.Exec(templateVal(p, initStatements)); err != nil {
		return errgo.Notef(err, "cannot initialize table")
	}
	if err := s.prepareAll(p); err != nil {
		return errgo.Notef(err, "cannot prepare statements")
	}
	return nil
}

func (s *RootKeys) prepareAll(p *templateParams) error {
	if err := s.prepareFindId(p); err != nil {
		return errgo.Mask(err)
	}
	if err := s.prepareFindBestRootKey(p); err != nil {
		return errgo.Mask(err)
	}
	if err := s.prepareInsertKey(p); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

func (s *RootKeys) prepareFindId(p *templateParams) error {
	return s.prepare(findIdStmt, p, `
SELECT id, created, expires, rootkey FROM {{.Table}} WHERE id=$1
`)
}

func (s *RootKeys) getKey(id []byte) (dbrootkeystore.RootKey, error) {
	if err := s.initDB(); err != nil {
		return dbrootkeystore.RootKey{}, errgo.Mask(err)
	}
	var key dbrootkeystore.RootKey
	err := s.stmts[findIdStmt].QueryRow(id).Scan(
		&key.Id,
		&key.Created,
		&key.Expires,
		&key.RootKey,
	)
	switch {
	case err == sql.ErrNoRows:
		return dbrootkeystore.RootKey{}, bakery.ErrNotFound
	case err != nil:
		return dbrootkeystore.RootKey{}, errgo.Mask(err)
	}
	return key, nil
}

func (s *RootKeys) prepareFindBestRootKey(p *templateParams) error {
	return s.prepare(findBestRootKeyStmt, p, `
SELECT id, created, expires, rootkey FROM {{.Table}}
WHERE
	created >= $1 AND
	expires >= $2 AND
	expires <= $3
ORDER BY created DESC
`)
}

func (s *RootKeys) findLatestKey(createdAfter, expiresAfter, expiresBefore time.Time) (dbrootkeystore.RootKey, error) {
	if err := s.initDB(); err != nil {
		return dbrootkeystore.RootKey{}, errgo.Mask(err)
	}
	var key dbrootkeystore.RootKey
	err := s.stmts[findBestRootKeyStmt].QueryRow(
		createdAfter,
		expiresAfter,
		expiresBefore,
	).Scan(
		&key.Id,
		&key.Created,
		&key.Expires,
		&key.RootKey,
	)
	if err == sql.ErrNoRows || err == nil {
		return key, nil
	}
	return dbrootkeystore.RootKey{}, errgo.Mask(err)
}

func (s *RootKeys) prepareInsertKey(p *templateParams) error {
	return s.prepare(insertKeyStmt, p, `
INSERT into {{.Table}} (id, rootkey, created, expires) VALUES ($1, $2, $3, $4)
`)
}

func (s *RootKeys) insertKey(key dbrootkeystore.RootKey) error {
	if err := s.initDB(); err != nil {
		return errgo.Mask(err)
	}
	_, err := s.stmts[insertKeyStmt].Exec(key.Id, key.RootKey, key.Created, key.Expires)
	return errgo.Mask(err)
}

func (s *RootKeys) prepare(id stmtId, p *templateParams, tmpl string) error {
	if s.stmts[id] != nil {
		panic(fmt.Sprintf("statement %v prepared twice", id))
	}
	stmt, err := s.db.Prepare(templateVal(p, tmpl))
	if err != nil {
		return errgo.Notef(err, "statement %v (%q) invalid", id, templateVal(p, tmpl))
	}
	s.stmts[id] = stmt
	return nil
}

func templateVal(p *templateParams, s string) string {
	tmpl := template.Must(template.New("").Parse(s))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		panic(errgo.Notef(err, "cannot create initialization statements"))
	}
	return buf.String()
}
