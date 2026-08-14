package syncprotocol

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

const MaxRevisionCoreBytes = 4 << 20

var (
	encodeMode = mustEncodeMode()
	decodeMode = mustDecodeMode()
)

func EncodeRevisionCore(core RevisionCore) ([]byte, error) {
	core.normalize()
	if err := core.Validate(); err != nil {
		return nil, err
	}
	return encodeMode.Marshal(core)
}

func DecodeRevisionCore(data []byte) (RevisionCore, error) {
	var core RevisionCore
	if len(data) == 0 || len(data) > MaxRevisionCoreBytes {
		return core, fmt.Errorf("revision core size must be between 1 and %d bytes", MaxRevisionCoreBytes)
	}
	if err := decodeMode.Unmarshal(data, &core); err != nil {
		return core, fmt.Errorf("decode revision core: %w", err)
	}
	core.normalize()
	if err := core.Validate(); err != nil {
		return core, err
	}
	canonical, err := encodeMode.Marshal(core)
	if err != nil {
		return core, err
	}
	if !bytes.Equal(data, canonical) {
		return core, errors.New("revision core is not deterministic CBOR")
	}
	return core, nil
}

func mustEncodeMode() cbor.EncMode {
	options := cbor.CoreDetEncOptions()
	options.NilContainers = cbor.NilContainerAsEmpty
	options.TagsMd = cbor.TagsForbidden
	options.ByteArray = cbor.ByteArrayToByteSlice
	options.BinaryMarshaler = cbor.BinaryMarshalerNone
	options.TextMarshaler = cbor.TextMarshalerNone
	mode, err := options.EncMode()
	if err != nil {
		panic(err)
	}
	return mode
}

func mustDecodeMode() cbor.DecMode {
	mode, err := (cbor.DecOptions{
		DupMapKey:         cbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels:   16,
		MaxArrayElements:  4096,
		MaxMapPairs:       32,
		IndefLength:       cbor.IndefLengthForbidden,
		TagsMd:            cbor.TagsForbidden,
		ExtraReturnErrors: cbor.ExtraDecErrorUnknownField,
		UTF8:              cbor.UTF8RejectInvalid,
		FieldNameMatching: cbor.FieldNameMatchingCaseSensitive,
		NaN:               cbor.NaNDecodeForbidden,
		Inf:               cbor.InfDecodeForbidden,
		BignumTag:         cbor.BignumTagForbidden,
		BinaryUnmarshaler: cbor.BinaryUnmarshalerNone,
		TextUnmarshaler:   cbor.TextUnmarshalerNone,
	}).DecMode()
	if err != nil {
		panic(err)
	}
	return mode
}
