package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// readDefinitionFile does all file work and validation before a writable open.
func readDefinitionFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if _, err := definition.Read(bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	return raw, nil
}

func definitionFailure(err error) int {
	var occupied *definition.OccupiedStatesError
	if errors.As(err, &occupied) {
		return usagef("%v", err)
	}
	return storeFailure(err)
}

func definitionFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("definition")
	path := fs.String("store", "", "existing store whose definition to read or replace")
	input := fs.String("set", "", "validated definition file to replace the stored declaration")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" {
		return record.Envelope{}, usagef("definition needs --store <path>")
	}
	var content []byte
	if *input != "" {
		var err error
		content, err = readDefinitionFile(*input)
		if err != nil {
			return record.Envelope{}, usagef("%v", err)
		}
	}
	ctx := context.Background()
	if env, code := schemaGate(ctx, *path, "definition"); code != record.ExitOK {
		return env, code
	}
	var s *store.Store
	var err error
	if *input == "" {
		s, err = store.OpenRead(ctx, *path)
	} else {
		s, err = store.Open(ctx, *path, false)
	}
	if err != nil {
		return record.Envelope{}, openFailure(err)
	}
	defer s.Close()
	var result definition.Record
	if *input == "" {
		result, err = definition.ForStore(ctx, s)
	} else {
		result, err = definition.Set(ctx, s, content)
	}
	if err != nil {
		return record.Envelope{}, definitionFailure(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return record.Envelope{}, storeFailure(err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion, Kind: "store.definition", ID: newID(),
		Producer:  record.Producer{System: record.System, Utility: utility, Version: version},
		CreatedAt: time.Now().UTC(), Payload: raw,
	}, record.ExitOK
}
