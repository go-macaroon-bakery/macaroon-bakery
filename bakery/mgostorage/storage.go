// Package mgostorage provides an implementation of the
// bakery Storage interface that uses MongoDB to store
// items.
package mgostorage

import (
	"github.com/juju/loggo"
	"github.com/juju/mgosession"
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"

	"gopkg.in/macaroon-bakery.v1/bakery"
)

type mgoSessionPool interface {
	Close()
	Session(mgosession.Logger) *mgo.Session
}

// New returns an implementation of Storage
// that stores all items in MongoDB.
// It never returns an error (the error return
// is for backward compatibility with a previous
// version that could return an error).
//
// Note that the caller is responsible for closing
// the mgo session associated with the collection.
func New(pool mgoSessionPool, db, collection string) (bakery.Storage, error) {
	return mgoStorage{
		pool: pool,
		db:   db,
		c:    collection,
	}, nil
}

type mgoStorage struct {
	pool mgoSessionPool
	db   string
	c    string
}

type storageDoc struct {
	Location string `bson:"_id"`
	Item     string `bson:"item"`

	// OldLocation is set for backward compatibility reasons - the
	// original version of the code used "loc" as a unique index
	// so we need to maintain the uniqueness otherwise
	// inserts will fail.
	// TODO remove this when moving to bakery.v2.
	OldLocation string `bson:"loc"`
}

func (s mgoStorage) collection() (*mgo.Collection, func()) {
	session := s.pool.Session(&poolLogger{logger})
	return session.DB(s.db).C(s.c), session.Close
}

// Put implements bakery.Storage.Put.
func (s mgoStorage) Put(location, item string) error {
	col, cleanup := s.collection()
	defer cleanup()
	i := storageDoc{
		Location:    location,
		OldLocation: location,
		Item:        item,
	}
	_, err := col.UpsertId(location, i)
	if err != nil {
		return errgo.Notef(err, "cannot store item for location %q", location)
	}
	return nil
}

// Get implements bakery.Storage.Get.
func (s mgoStorage) Get(location string) (string, error) {
	col, cleanup := s.collection()
	defer cleanup()
	var i storageDoc
	err := col.FindId(location).One(&i)
	if err != nil {
		if err == mgo.ErrNotFound {
			return "", bakery.ErrNotFound
		}
		return "", errgo.Notef(err, "cannot get %q", location)
	}

	return i.Item, nil
}

// Del implements bakery.Storage.Del.
func (s mgoStorage) Del(location string) error {
	col, cleanup := s.collection()
	defer cleanup()
	err := col.RemoveId(location)
	if err != nil {
		return errgo.Notef(err, "cannot remove %q", location)
	}
	return nil
}

type poolLogger struct {
	logger loggo.Logger
}

// Debug implements the mgosession.Logger interface.
func (l *poolLogger) Debug(message string) {
	l.logger.Debugf(message)
}

// Info implements the mgosession.Logger interface.
func (l *poolLogger) Info(message string) {
	l.logger.Infof(message)
}
