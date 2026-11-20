package main

import (
	"encoding/json"
	"errors"
	"io"
)

// Keep transport causes available for classification without showing response
// fragments, URLs or other backend details in the user-facing error message.
type engineResponseError struct {
	message string
	cause   error
}

func (e *engineResponseError) Error() string { return e.message }
func (e *engineResponseError) Unwrap() error { return e.cause }

func decodeEngineResponse(input io.Reader, limit int64) (map[string]any, error) {
	reader := &io.LimitedReader{R: input, N: limit + 1}
	decoder := json.NewDecoder(reader)
	var result map[string]any
	err := decoder.Decode(&result)
	if reader.N <= 0 || decoder.InputOffset() > limit {
		return nil, errors.New("引擎结果超过大小上限，请按子目录浏览或减少目录规模")
	}
	if err != nil || result == nil {
		return nil, &engineResponseError{"引擎响应无法解析", err}
	}
	var extra any
	err = decoder.Decode(&extra)
	if reader.N <= 0 {
		return nil, errors.New("引擎结果超过大小上限，请按子目录浏览或减少目录规模")
	}
	if err != io.EOF {
		return nil, &engineResponseError{"引擎响应包含额外内容，无法解析", err}
	}
	return result, nil
}
