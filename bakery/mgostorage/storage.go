// Package mgostorage provides an implementation of the
// bakery Storage interface that uses MongoDB to store
// items.
package mgostorage

import (
	"github.com/juju/errgo"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/go-macaroon-bakery/macaroon-bakery/bakery"
)

// New returns an implementation of Storage
// that stores all items in MongoDB.
func New(c *mgo.Collection) *mgoStorage {
	m := mgoStorage{
		col: c,
	}
	m.setupCollection()
	return &m
}

type mgoStorage struct {
	col *mgo.Collection
}

type storageDoc struct {
	Location string `bson:"loc"`
	Item     string `bson:"item"`
}

func (s *mgoStorage) setupCollection() error {
	collection := s.collection()
	defer collection.Close()

	return collection.EnsureIndex(mgo.Index{Key: []string{"loc"}, Unique: true})
}

// collection returns the collection with a copied mgo session.
// It must be closed when done with.
func (m *mgoStorage) collection() collectionCloser {
	c := m.col.Database.Session.Copy().DB(m.col.Database.Name).C(m.col.Name)
	return collectionCloser{c}
}

type collectionCloser struct {
	*mgo.Collection
}

func (c collectionCloser) Close() {
	c.Collection.Database.Session.Close()
}

// Put implements the bakery Storage interface.
func (s mgoStorage) Put(location, item string) error {
	i := storageDoc{Location: location, Item: item}

	collection := s.collection()
	defer collection.Close()

	_, err := collection.Upsert(bson.M{"loc": location}, i)
	if err != nil {
		return errgo.Notef(err, "cannot store item for location %q", location)
	}
	return nil
}

// Get implements the bakery Storage interface.
func (s mgoStorage) Get(location string) (string, error) {
	collection := s.collection()
	defer collection.Close()

	var i storageDoc
	err := collection.Find(bson.M{"loc": location}).One(&i)
	if err != nil {
		if err == mgo.ErrNotFound {
			return "", bakery.ErrNotFound
		}
		return "", errgo.Notef(err, "cannot get %q", location)
	}

	return i.Item, nil
}

// Del implements the bakery Storage interface.
func (s mgoStorage) Del(location string) error {
	collection := s.collection()
	defer collection.Close()

	err := collection.Remove(bson.M{"loc": location})
	if err != nil {
		return errgo.Notef(err, "cannot remove %q", location)
	}
	return nil
}
