// Package mgostorage provides an implementation of the
// bakery Storage interface that uses MongoDB to store
// items.
package mgostorage

import (
	"github.com/juju/errgo"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/macaroon-bakery.v0/bakery"
)

// New returns an implementation of Storage
// that stores all items in MongoDB.
func New(c *mgo.Collection) (bakery.Storage, error) {
	m := mgoStorage{
		col: c,
	}
	err := m.setUpCollection()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return &m, nil
}

type mgoStorage struct {
	col *mgo.Collection
}

type storageDoc struct {
	Location string `bson:"loc"`
	Item     string `bson:"item"`
}

func (s *mgoStorage) setUpCollection() error {
	collection := s.collection()
	defer collection.Close()
	err := collection.EnsureIndex(mgo.Index{Key: []string{"loc"}, Unique: true})
	if err != nil {
		return errgo.Notef(err, "failed to ensure an index on loc exists")
	}
	return nil
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

// Put implements bakery.Storage.Put.
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

// Get implements bakery.Storage.Get.
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

// Del implements bakery.Storage.Del.
func (s mgoStorage) Del(location string) error {
	collection := s.collection()
	defer collection.Close()

	err := collection.Remove(bson.M{"loc": location})
	if err != nil {
		return errgo.Notef(err, "cannot remove %q", location)
	}
	return nil
}
