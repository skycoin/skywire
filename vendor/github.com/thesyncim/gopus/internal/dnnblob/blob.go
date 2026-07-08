// Package dnnblob parses and validates the libopus USE_WEIGHTS_FILE neural
// weights blob and exposes typed views over its records.
//
// A blob is the on-disk WeightArray container libopus loads with
// parse_weights() (dnn/parse_lpcnet_weights.c): a sequence of named records,
// each carrying a type tag and a payload of float32, int32 or int8 weights. The
// DRED, OSCE and LPCNet model loaders bind their layers from these records, so
// this package mirrors the libopus blob format and record types exactly.
package dnnblob

import (
	"encoding/binary"
	"slices"
	"sort"
	"strings"
)

const headerSize = 64

// Record type tags, matching the libopus WeightArray type field
// (dnn/nnet.h WEIGHT_TYPE_*): float32, int32, quantized weights and int8.
const (
	TypeFloat   int32 = 0
	TypeInt     int32 = 1
	TypeQWeight int32 = 2
	TypeInt8    int32 = 3
)

// Record mirrors one libopus WeightArray entry parsed from a weights blob.
type Record struct {
	Name string
	Type int32
	Size int32
	Data []byte
}

// Blob stores a validated copy of a libopus-style weights blob and its records.
type Blob struct {
	Raw     []byte
	Records []Record
}

// DecoderModelState summarizes which decoder-side model families are present in
// a validated weights blob.
type DecoderModelState struct {
	PitchDNN bool
	PLC      bool
	FARGAN   bool
	DRED     bool
	OSCE     bool
	OSCEBWE  bool
}

var (
	requiredDecoderControlCoreRecordNames = sortedRecordNames(
		pitchDNNRequiredRecordNames,
		plcRequiredRecordNames,
		farganRequiredRecordNames,
	)
	requiredDecoderControlWithBWERecordNames = sortedRecordNames(
		pitchDNNRequiredRecordNames,
		plcRequiredRecordNames,
		farganRequiredRecordNames,
		osceLACERequiredRecordNames,
		osceNoLACERequiredRecordNames,
		osceBWERequiredRecordNames,
	)
	requiredEncoderControlRecordNames = sortedRecordNames(
		pitchDNNRequiredRecordNames,
		dredEncoderRequiredRecordNames,
	)
	requiredStandaloneDREDDecoderRecordNames = sortedRecordNames(dredDecoderRequiredRecordNames)
)

// Clone validates data using libopus parse_weights-style framing rules and
// returns a persistent copy whose record slices point into the copied buffer.
func Clone(data []byte) (*Blob, error) {
	raw := append([]byte(nil), data...)
	records, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return &Blob{Raw: raw, Records: records}, nil
}

// HasRecord reports whether the parsed blob contains a record with the given name.
func (b *Blob) HasRecord(name string) bool {
	if b == nil {
		return false
	}
	for _, rec := range b.Records {
		if rec.Name == name {
			return true
		}
	}
	return false
}

// Record returns the first record with the given name, mirroring libopus
// find_array_entry() first-match behavior.
func (b *Blob) Record(name string) (Record, bool) {
	if b == nil {
		return Record{}, false
	}
	for _, rec := range b.Records {
		if rec.Name == name {
			return rec, true
		}
	}
	return Record{}, false
}

func sortedRecordNames(groups ...[]string) []string {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	present := make(map[string]struct{}, total)
	for _, group := range groups {
		for _, name := range group {
			present[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(present))
	for name := range present {
		if before, ok := strings.CutSuffix(name, "_weights_float"); ok {
			int8Name := before + "_weights_int8"
			if _, ok := present[int8Name]; ok {
				continue
			}
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (b *Blob) validateRecordNames(required []string) error {
	for _, want := range required {
		if optionalFloatMirror(required, want) {
			continue
		}
		if !b.HasRecord(want) {
			return errInvalidBlob
		}
	}
	return nil
}

func optionalFloatMirror(required []string, name string) bool {
	if !strings.HasSuffix(name, "_weights_float") {
		return false
	}
	int8Name := strings.TrimSuffix(name, "_weights_float") + "_weights_int8"
	return slices.Contains(required, int8Name)
}

// SupportsPitchDNN reports whether the blob contains the pitch model family
// libopus uses from both DRED and PLC/FARGAN control loaders.
func (b *Blob) SupportsPitchDNN() bool {
	return b.validateRecordNames(pitchDNNRequiredRecordNames) == nil
}

// SupportsPLC reports whether the blob contains the PLC model family.
func (b *Blob) SupportsPLC() bool {
	return b.validateRecordNames(plcRequiredRecordNames) == nil
}

// SupportsFARGAN reports whether the blob contains the FARGAN model family.
func (b *Blob) SupportsFARGAN() bool {
	return b.validateRecordNames(farganRequiredRecordNames) == nil
}

// SupportsDREDEncoder reports whether the blob contains the DRED encoder model family.
func (b *Blob) SupportsDREDEncoder() bool {
	return b.validateRecordNames(dredEncoderRequiredRecordNames) == nil
}

// SupportsDREDDecoder reports whether the blob contains the DRED decoder model family.
func (b *Blob) SupportsDREDDecoder() bool {
	return b.validateRecordNames(dredDecoderRequiredRecordNames) == nil
}

// SupportsOSCELACE reports whether the blob contains the LACE OSCE model family.
func (b *Blob) SupportsOSCELACE() bool {
	return b.validateRecordNames(osceLACERequiredRecordNames) == nil
}

// SupportsOSCENoLACE reports whether the blob contains the NoLACE OSCE model family.
func (b *Blob) SupportsOSCENoLACE() bool {
	return b.validateRecordNames(osceNoLACERequiredRecordNames) == nil
}

// SupportsOSCE reports whether the blob contains the core OSCE model families.
func (b *Blob) SupportsOSCE() bool {
	return b.SupportsOSCELACE() && b.SupportsOSCENoLACE()
}

// SupportsOSCEBWE reports whether the blob contains the OSCE_BWE model family.
func (b *Blob) SupportsOSCEBWE() bool {
	return b.validateRecordNames(osceBWERequiredRecordNames) == nil
}

// DecoderModels reports which decoder-side model families are available from
// the retained blob.
func (b *Blob) DecoderModels() DecoderModelState {
	return DecoderModelState{
		PitchDNN: b.SupportsPitchDNN(),
		PLC:      b.SupportsPLC(),
		FARGAN:   b.SupportsFARGAN(),
		DRED:     b.SupportsDREDDecoder(),
		OSCE:     b.SupportsOSCE(),
		OSCEBWE:  b.SupportsOSCEBWE(),
	}
}

// ValidateEncoderControl mirrors the libopus encoder DNN-blob surface by
// requiring the model families needed for DRED encoder loading.
func (b *Blob) ValidateEncoderControl() error {
	if !b.SupportsDREDEncoder() || !b.SupportsPitchDNN() {
		return errInvalidBlob
	}
	return nil
}

// ValidateDecoderControl mirrors the default-build libopus decoder DNN-blob
// surface by requiring the core deep-PLC model families and, when requested,
// the optional OSCE/OSCE_BWE families.
func (b *Blob) ValidateDecoderControl(requireOSCEBWE bool) error {
	models := b.DecoderModels()
	if !models.PLC || !models.PitchDNN || !models.FARGAN {
		return errInvalidBlob
	}
	if requireOSCEBWE && (!models.OSCE || !models.OSCEBWE) {
		return errInvalidBlob
	}
	return nil
}

// ValidateDREDDecoderControl mirrors the standalone libopus DRED decoder
// model-loading path, which only requires the RDOVAE decoder family.
func (b *Blob) ValidateDREDDecoderControl() error {
	if !b.SupportsDREDDecoder() {
		return errInvalidBlob
	}
	return nil
}

// RequiredDecoderControlRecordNames returns a read-only view of the
// loader-derived record names the default-build libopus main decoder path
// expects from OPUS_SET_DNN_BLOB. When requireOSCEBWE is true, the returned
// view also includes the optional OSCE and OSCE_BWE families.
func RequiredDecoderControlRecordNames(requireOSCEBWE bool) []string {
	if requireOSCEBWE {
		return requiredDecoderControlWithBWERecordNames
	}
	return requiredDecoderControlCoreRecordNames
}

// RequiredEncoderControlRecordNames returns a read-only view of the
// loader-derived record names the libopus encoder path expects from
// OPUS_SET_DNN_BLOB.
func RequiredEncoderControlRecordNames() []string {
	return requiredEncoderControlRecordNames
}

// RequiredDREDDecoderRecordNames returns a read-only view of the loader-derived
// record names for the standalone libopus DRED decoder model family.
func RequiredDREDDecoderRecordNames() []string {
	return requiredStandaloneDREDDecoderRecordNames
}

func parse(data []byte) ([]Record, error) {
	records := make([]Record, 0, 4)
	offset := 0
	for offset < len(data) {
		remaining := len(data) - offset
		if remaining < headerSize {
			return nil, errInvalidBlob
		}

		hdr := data[offset : offset+headerSize]
		typ := int32(binary.LittleEndian.Uint32(hdr[8:12]))
		size := int32(binary.LittleEndian.Uint32(hdr[12:16]))
		blockSize := int32(binary.LittleEndian.Uint32(hdr[16:20]))
		if size <= 0 || blockSize < size {
			return nil, errInvalidBlob
		}
		if int(blockSize) > remaining-headerSize {
			return nil, errInvalidBlob
		}

		nameBytes := hdr[20:64]
		if nameBytes[len(nameBytes)-1] != 0 {
			return nil, errInvalidBlob
		}
		nameLen := 0
		for nameLen < len(nameBytes) && nameBytes[nameLen] != 0 {
			nameLen++
		}
		dataStart := offset + headerSize
		dataEnd := dataStart + int(size)
		records = append(records, Record{
			Name: string(nameBytes[:nameLen]),
			Type: typ,
			Size: size,
			Data: data[dataStart:dataEnd],
		})
		offset += headerSize + int(blockSize)
	}
	if offset != len(data) {
		return nil, errInvalidBlob
	}
	return records, nil
}
