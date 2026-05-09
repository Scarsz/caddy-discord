package discord

import (
	"encoding/json"
	"io"
	"net/http"
)

func getBody[T any](r *http.Response) (*T, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	return unmarshalAny[T](body)
}

func unmarshalAny[T any](bytes []byte) (*T, error) {
	out := new(T)
	if err := json.Unmarshal(bytes, out); err != nil {
		return nil, err
	}

	return out, nil
}
