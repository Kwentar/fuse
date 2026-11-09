package storage

import "encoding/json"

func jsonBytes(v any) ([]byte, error) { b, e := json.Marshal(v); return append(b, '\n'), e }
