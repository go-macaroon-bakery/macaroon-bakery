// Package mgostorage provides an implementation of the
// bakery Storage interface that uses MongoDB to store
// items.
package mgostorage

import (
	"errors"
	"log"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/go-macaroon-bakery/macaroon-bakery/bakery"
)

const debug = false

func logf(f string, a ...interface{}) {
	if debug {
		log.Printf(f, a...)
	}
}

// NewMemStorage returns an implementation of Storage
// that stores all items in MongoDB.
func NewMgoStorage(s *mgo.Session, database, collection string) (*mgoStorage, error) {
	if s == nil || database == "" || collection == "" {
		return nil, errors.New("nil or empty input arguments")
	}
	return &mgoStorage{
		session: s,
		db:      database,
		c:       collection,
	}, nil
}

type mgoStorage struct {
	session *mgo.Session
	db      string
	c       string
}

type storageItem struct {
	Location string `bson:"location"`
	Item     string `bson:"item"`
}

func (s *mgoStorage) setupCollection() error {
	collection, close := s.itemsCollection()
	defer close()

	return collection.EnsureIndex(mgo.Index{Key: []string{"location"}, Unique: true})
}

// itemsCollection returns the items collection
func (m *mgoStorage) itemsCollection() (*mgo.Collection, func()) {
	s := m.session.Copy()
	return s.DB(m.db).C(m.c), s.Close
}

// Put implements the bakery Storage interface.
func (s mgoStorage) Put(location, item string) error {
	logf("storage.Put[%q] %q", location, item)

	i := storageItem{Location: location, Item: item}

	collection, close := s.itemsCollection()
	defer close()

	_, err := collection.Upsert(bson.M{"location": location}, i)
	if err != nil {
		logf("storage.Put: error storing an item item: %s", err.Error())
		return errors.New("storage.Put : error storing an item")
	}
	return nil
}

// Get implements the bakery Storage interface.
func (s mgoStorage) Get(location string) (string, error) {
	logf("storage.Get[%q]", location)
	collection, close := s.itemsCollection()
	defer close()

	var i storageItem
	err := collection.Find(bson.M{"location": location}).One(&i)
	if err != nil {
		if err == mgo.ErrNotFound {
			return "", bakery.ErrNotFound
		}
		logf("storage.Get[%q] -> error finding an item: %v", location, err)
		return "", errors.New("error finding an item for location " + location)
	}
	logf("storage.Get[%q] -> %q", location, i.Item)

	return i.Item, nil
}

// Del implements the bakery Storage interface.
func (s mgoStorage) Del(location string) error {
	logf("storage.Del[%q]", location)
	collection, close := s.itemsCollection()
	defer close()

	err := collection.Remove(bson.M{"location": location})
	if err != nil {
		logf("storage.Del[%q] -> error removing an item: %v", location, err)
		return errors.New("error removing an item for location " + location)
	}
	return nil
}
