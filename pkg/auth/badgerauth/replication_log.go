// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package badgerauth

import (
	"encoding/binary"

	badger "github.com/outcaste-io/badger/v3"
	"github.com/zeebo/errs"

	"storj.io/gateway-mt/pkg/auth/authdb"
	"storj.io/gateway-mt/pkg/auth/badgerauth/pb"
)

const (
	replicationLogPrefix    = "replication_log" + replicationLogEntrySeparator
	lenReplicationLogPrefix = len(replicationLogPrefix)

	replicationLogEntrySeparator    = "/"
	lenReplicationLogEntrySeparator = len(replicationLogEntrySeparator)

	lenKeyHash = len(authdb.KeyHash{})

	minLenReplicationLogEntry = lenReplicationLogPrefix + 3*lenReplicationLogEntrySeparator + lenKeyHash + 8 + 4
)

// ReplicationLogError is a class of replication log errors.
var ReplicationLogError = errs.Class("replication log")

// ReplicationLogEntry represents replication log entry.
//
// Key layout reference:
// https://github.com/storj/gateway-mt/blob/3ef75f412a50118d9d910e1b372e126e6ffb7503/docs/blueprints/new-auth-database.md#replication-log-entry
type ReplicationLogEntry struct {
	ID      NodeID
	Clock   Clock
	KeyHash authdb.KeyHash
	State   pb.Record_State
}

// Bytes returns a slice of bytes.
func (e ReplicationLogEntry) Bytes() []byte {
	var stateBytes [4]byte
	binary.BigEndian.PutUint32(stateBytes[:], uint32(e.State))

	key := make([]byte, 0, minLenReplicationLogEntry+len(e.ID))
	key = append(key, replicationLogPrefix...)
	key = append(key, e.ID.Bytes()...)
	key = append(key, replicationLogEntrySeparator...)
	key = append(key, e.Clock.Bytes()...)
	key = append(key, replicationLogEntrySeparator...)
	key = append(key, e.KeyHash[:]...)
	key = append(key, replicationLogEntrySeparator...)
	key = append(key, stateBytes[:]...)

	return key
}

// ToBadgerEntry constructs new *badger.Entry from e.
func (e ReplicationLogEntry) ToBadgerEntry() *badger.Entry {
	return badger.NewEntry(e.Bytes(), nil)
}

// SetBytes parses entry as ReplicationLogEntry and sets entry's value to result.
func (e *ReplicationLogEntry) SetBytes(entry []byte) error {
	// Make sure we don't keep a reference to the input entry.
	entry = append([]byte{}, entry...)

	if len(entry) < minLenReplicationLogEntry {
		return ReplicationLogError.New("entry too short")
	}

	entry = entry[lenReplicationLogPrefix:] // trim leftmost replicationLogPrefix
	stateBytes, entry := entry[len(entry)-4:], entry[:len(entry)-4]
	entry = entry[:len(entry)-lenReplicationLogEntrySeparator] // trim rightmost separator
	keyHash, entry := entry[len(entry)-lenKeyHash:], entry[:len(entry)-lenKeyHash]
	entry = entry[:len(entry)-lenReplicationLogEntrySeparator] // trim rightmost separator
	clockBytes, entry := entry[len(entry)-8:], entry[:len(entry)-8]
	entry = entry[:len(entry)-lenReplicationLogEntrySeparator] // trim rightmost separator
	idBytes := entry                                           // ID is the remainder

	if err := e.Clock.SetBytes(clockBytes); err != nil {
		return ReplicationLogError.Wrap(err)
	}

	if err := e.ID.SetBytes(idBytes); err != nil {
		return ReplicationLogError.Wrap(err)
	}

	e.KeyHash = *(*[32]byte)(keyHash)
	e.State = pb.Record_State(binary.BigEndian.Uint32(stateBytes))

	return nil
}

func findReplicationLogEntriesByKeyHash(txn *badger.Txn, keyHash authdb.KeyHash) ([]ReplicationLogEntry, error) {
	var entries []ReplicationLogEntry

	opt := badger.DefaultIteratorOptions      // TODO(artur): should we also set SinceTs?
	opt.PrefetchValues = false                // fasten your seatbelts; see: https://dgraph.io/docs/badger/get-started/#key-only-iteration
	opt.Prefix = []byte(replicationLogPrefix) // don't roll through everything

	it := txn.NewIterator(opt)
	defer it.Close()
	for it.Rewind(); it.Valid(); it.Next() {
		var entry ReplicationLogEntry
		if err := entry.SetBytes(it.Item().Key()); err != nil {
			return nil, err
		}
		if keyHash == entry.KeyHash {
			// Normally, we would have to call KeyCopy to append the key to use
			// it outside of iteration, but SetBytes is already safe in the
			// sense that it copies.
			entries = append(entries, entry)
		}
	}

	return entries, nil
}
