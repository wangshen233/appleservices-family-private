package gsa

import (
	"errors"
	"io"
	"net/http"
)

func readAccountResponse(response *http.Response) ([]byte, error) {
	const limit = 2 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, errors.New("gsa: could not read account-device response")
	}
	if len(data) > limit {
		return nil, errors.New("gsa: account-device response too large")
	}
	return data, nil
}
