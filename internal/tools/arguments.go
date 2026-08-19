package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func decodeToolArguments(arguments json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArguments, err)
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: arguments contain multiple JSON values", ErrInvalidArguments)
		}
		return fmt.Errorf("%w: %w", ErrInvalidArguments, err)
	}
	return nil
}
