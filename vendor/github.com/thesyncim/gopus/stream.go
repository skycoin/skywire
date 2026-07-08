// stream.go implements streaming io.Reader and io.WriteCloser wrappers for Opus encoding/decoding.

package gopus

import (
	"encoding/binary"
	"io"
	"math"
	"unsafe"
)

// hostIsLittleEndian reports whether the running host stores multibyte values
// little-endian. Detected once at init via a uint16 byte view; on LE hosts the
// streaming Reader can copy decoded PCM straight to its output buffer instead
// of re-encoding each sample.
var hostIsLittleEndian = func() bool {
	var x uint16 = 1
	return *(*byte)(unsafe.Pointer(&x)) == 1
}()

// Streaming API
//
// The Reader and Writer types provide io.Reader and io.WriteCloser interfaces
// for streaming Opus encode/decode operations. They handle frame boundaries
// internally, allowing integration with Go's standard io patterns.
//
// # Streaming Decode
//
// To decode a stream of Opus packets:
//
//	source := &MyPacketReader{} // implements PacketReader
//	reader, err := gopus.NewReader(gopus.DefaultDecoderConfig(48000, 2), source, gopus.FormatFloat32LE)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Read decoded PCM bytes
//	buf := make([]byte, 4096)
//	for {
//	    n, err := reader.Read(buf)
//	    if err == io.EOF {
//	        break
//	    }
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    processAudio(buf[:n])
//	}
//
// # Streaming Encode
//
// To encode PCM audio to a stream of Opus packets:
//
//	sink := &MyPacketSink{} // implements PacketSink
//	writer, err := gopus.NewWriter(48000, 2, sink, gopus.FormatFloat32LE, gopus.ApplicationAudio)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Write PCM bytes
//	pcmBytes := getPCMData() // float32 little-endian bytes
//	_, err = writer.Write(pcmBytes)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Flush remaining buffered samples and close the sink when supported.
//	if err := writer.Close(); err != nil {
//	    log.Fatal(err)
//	}
//
// # Sample Format
//
// Both Reader and Writer support two sample formats:
//   - FormatFloat32LE: 32-bit float, little-endian (4 bytes per sample)
//   - FormatInt16LE: 16-bit signed integer, little-endian (2 bytes per sample)
//
// Samples are interleaved for stereo: [L0, R0, L1, R1, ...]

// SampleFormat specifies the PCM sample format for streaming.
type SampleFormat int

const (
	// FormatFloat32LE is 32-bit float, little-endian (4 bytes per sample).
	FormatFloat32LE SampleFormat = iota
	// FormatInt16LE is 16-bit signed integer, little-endian (2 bytes per sample).
	FormatInt16LE
)

func validSampleFormat(format SampleFormat) bool {
	switch format {
	case FormatFloat32LE, FormatInt16LE:
		return true
	default:
		return false
	}
}

// BytesPerSample returns the number of bytes per sample for the format.
func (f SampleFormat) BytesPerSample() int {
	switch f {
	case FormatFloat32LE:
		return 4
	case FormatInt16LE:
		return 2
	default:
		return 0
	}
}

// PacketReader provides Opus packets for streaming decode.
// Implementations should return io.EOF when no more packets are available.
type PacketReader interface {
	// ReadPacketInto fills dst with the next Opus packet.
	//
	// granulePos is the source packet position in decoded-sample units when the
	// container provides one (for example Ogg Opus granule positions). Sources
	// that do not track positions should return 0.
	//
	// Returns io.EOF when stream ends. Return n=0, err=nil to trigger PLC.
	ReadPacketInto(dst []byte) (n int, granulePos uint64, err error)
}

// PacketSink receives encoded Opus packets from streaming encode.
type PacketSink interface {
	// WritePacket writes an encoded Opus packet.
	// Returns number of bytes written and any error.
	//
	// If the sink also implements io.Closer, Writer.Close forwards to it after
	// flushing any buffered audio.
	WritePacket(packet []byte) (int, error)
}

// Reader decodes an Opus stream, implementing io.Reader.
// Output is PCM samples in the configured format.
//
// The Reader handles frame boundaries internally, buffering decoded
// PCM samples and serving byte-oriented reads.
//
// Example:
//
//	reader, err := gopus.NewReader(gopus.DefaultDecoderConfig(48000, 2), source, gopus.FormatFloat32LE)
//	io.Copy(audioOutput, reader)
type Reader struct {
	dec    *Decoder
	source PacketReader
	format SampleFormat // Output sample format

	packetBuf      []byte
	pcmFloat       []float32 // Decoded PCM samples
	pcmInt16       []int16
	byteBuf        []byte // PCM as bytes
	offset         int    // Current read position in byteBuf
	lastGranulePos uint64 // Most recent packet position reported by the source

	eof bool // Source exhausted
}

// NewReader creates a streaming decoder.
//
// Parameters:
//   - cfg: decoder configuration
//   - source: provides Opus packets for decoding
//   - format: output sample format (FormatFloat32LE or FormatInt16LE)
func NewReader(cfg DecoderConfig, source PacketReader, format SampleFormat) (*Reader, error) {
	if source == nil {
		return nil, ErrNilPacketReader
	}
	if !validSampleFormat(format) {
		return nil, ErrInvalidSampleFormat
	}

	dec, err := NewDecoder(cfg)
	if err != nil {
		return nil, err
	}

	return &Reader{
		dec:       dec,
		source:    source,
		format:    format,
		packetBuf: make([]byte, dec.maxPacketBytes),
		pcmFloat:  make([]float32, dec.maxPacketSamples*int(dec.channels)),
		pcmInt16:  make([]int16, dec.maxPacketSamples*int(dec.channels)),
		offset:    0,
		eof:       false,
	}, nil
}

// Read implements io.Reader, reading decoded PCM bytes.
//
// The Reader handles frame boundaries internally, fetching and decoding
// packets as needed to fill the buffer.
func (r *Reader) Read(p []byte) (int, error) {
	// If buffer is exhausted, try to get more data
	if r.offset >= len(r.byteBuf) {
		if r.eof {
			return 0, io.EOF
		}

		// Fetch next packet from source
		nPacket, granulePos, err := r.source.ReadPacketInto(r.packetBuf)
		if err == io.EOF {
			r.eof = true
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		r.lastGranulePos = granulePos

		var packet []byte
		if nPacket > 0 {
			packet = r.packetBuf[:nPacket]
		}

		switch r.format {
		case FormatFloat32LE:
			nSamples, decErr := r.dec.Decode(packet, r.pcmFloat)
			if decErr != nil {
				return 0, decErr
			}
			nTotal := nSamples * int(r.dec.channels)
			byteLen := nTotal * 4
			if cap(r.byteBuf) < byteLen {
				r.byteBuf = make([]byte, byteLen)
			}
			r.byteBuf = r.byteBuf[:byteLen]
			if hostIsLittleEndian && nTotal > 0 {
				// On LE hosts the in-memory float32 layout already matches
				// FormatFloat32LE on the wire; copy the bytes directly.
				raw := unsafe.Slice((*byte)(unsafe.Pointer(&r.pcmFloat[0])), nTotal*4)
				copy(r.byteBuf, raw)
			} else {
				for i := range nTotal {
					bits := math.Float32bits(r.pcmFloat[i])
					binary.LittleEndian.PutUint32(r.byteBuf[i*4:], bits)
				}
			}
		case FormatInt16LE:
			nSamples, decErr := r.dec.DecodeInt16(packet, r.pcmInt16)
			if decErr != nil {
				return 0, decErr
			}
			nTotal := nSamples * int(r.dec.channels)
			byteLen := nTotal * 2
			if cap(r.byteBuf) < byteLen {
				r.byteBuf = make([]byte, byteLen)
			}
			r.byteBuf = r.byteBuf[:byteLen]
			if hostIsLittleEndian && nTotal > 0 {
				raw := unsafe.Slice((*byte)(unsafe.Pointer(&r.pcmInt16[0])), nTotal*2)
				copy(r.byteBuf, raw)
			} else {
				for i := range nTotal {
					binary.LittleEndian.PutUint16(r.byteBuf[i*2:], uint16(r.pcmInt16[i]))
				}
			}
		default:
			return 0, ErrInvalidSampleFormat
		}

		r.offset = 0
	}

	// Copy available bytes to p
	n := copy(p, r.byteBuf[r.offset:])
	r.offset += n

	return n, nil
}

// SampleRate returns the sample rate in Hz.
func (r *Reader) SampleRate() int {
	return r.dec.SampleRate()
}

// Channels returns the number of audio channels (1 or 2).
func (r *Reader) Channels() int {
	return r.dec.Channels()
}

// LastGranulePos returns the most recent packet position reported by the source.
//
// For Ogg Opus this is the granule position from the underlying page header.
// Sources that do not track positions may leave this at 0.
func (r *Reader) LastGranulePos() uint64 {
	return r.lastGranulePos
}

// Reset clears buffers and decoder state for a new stream.
func (r *Reader) Reset() {
	r.dec.Reset()
	if r.byteBuf != nil {
		r.byteBuf = r.byteBuf[:0]
	}
	r.offset = 0
	r.lastGranulePos = 0
	r.eof = false
}

// Writer encodes PCM samples to an Opus stream, implementing io.WriteCloser.
// Input is PCM samples in the configured format.
//
// The Writer buffers input samples until a complete frame is accumulated,
// then encodes and sends the packet to the sink.
//
// Example:
//
//	writer, err := gopus.NewWriter(48000, 2, sink, gopus.FormatFloat32LE, gopus.ApplicationAudio)
//	io.Copy(writer, audioInput)
//	writer.Close() // flush remaining buffered samples
type Writer struct {
	enc    *Encoder
	sink   PacketSink
	format SampleFormat // Input sample format

	sampleBuf    []byte // Buffered input bytes
	frameBytes   int    // Bytes needed for one frame
	frameSamples int    // Samples per frame across all channels

	packetBuf  []byte    // Buffer for encoded packet (4000 bytes)
	pcmScratch []float32 // Reused PCM scratch for byte-to-sample conversion
	paddedBuf  []byte    // Reused zero-padded frame buffer for Flush
	closed     bool
}

// NewWriter creates a streaming encoder.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000)
//   - channels: number of audio channels (1 or 2)
//   - sink: receives encoded Opus packets
//   - format: input sample format (FormatFloat32LE or FormatInt16LE)
//   - application: encoder application hint
func NewWriter(sampleRate, channels int, sink PacketSink, format SampleFormat, application Application) (*Writer, error) {
	if sink == nil {
		return nil, ErrNilPacketSink
	}
	if !validSampleFormat(format) {
		return nil, ErrInvalidSampleFormat
	}

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: application})
	if err != nil {
		return nil, err
	}

	// Default frame size is 960 samples (20ms at 48kHz)
	frameSize := enc.FrameSize()
	bytesPerSample := format.BytesPerSample()
	frameBytes := frameSize * channels * bytesPerSample
	frameSamples := frameSize * channels

	return &Writer{
		enc:          enc,
		sink:         sink,
		format:       format,
		sampleBuf:    make([]byte, 0, frameBytes*2), // Pre-allocate for 2 frames
		frameBytes:   frameBytes,
		frameSamples: frameSamples,
		packetBuf:    make([]byte, 4000),
		pcmScratch:   make([]float32, frameSamples),
		paddedBuf:    make([]byte, frameBytes),
	}, nil
}

// Write implements io.Writer, encoding PCM bytes to Opus packets.
//
// The Writer buffers input samples until a complete frame is accumulated,
// then encodes and sends the packet to the sink.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}

	initialBuffered := len(w.sampleBuf)
	processedBytes := 0
	// Append input to buffer
	w.sampleBuf = append(w.sampleBuf, p...)

	// Process complete frames
	for len(w.sampleBuf)-processedBytes >= w.frameBytes {
		// Extract one frame of bytes
		frameData := w.sampleBuf[processedBytes : processedBytes+w.frameBytes]

		// Convert bytes to float32 PCM using reusable scratch.
		pcm := w.pcmScratch[:w.frameSamples]
		w.decodePCMInto(pcm, frameData)

		// Encode the frame
		n, err := w.enc.Encode(pcm, w.packetBuf)
		if err != nil {
			w.discardConsumedPrefix(processedBytes)
			return consumedInputBytes(initialBuffered, processedBytes, len(p)), err
		}

		// If n > 0, send packet to sink (n == 0 means DTX suppressed)
		if n > 0 {
			if err := w.writePacketToSink(w.packetBuf[:n]); err != nil {
				w.closed = true
				w.discardConsumedPrefix(processedBytes)
				return consumedInputBytes(initialBuffered, processedBytes, len(p)), err
			}
		}

		processedBytes += w.frameBytes
	}

	w.discardConsumedPrefix(processedBytes)
	return len(p), nil
}

func consumedInputBytes(initialBuffered, processedBytes, incoming int) int {
	if processedBytes <= initialBuffered {
		return 0
	}
	consumed := processedBytes - initialBuffered
	if consumed > incoming {
		return incoming
	}
	return consumed
}

func (w *Writer) writePacketToSink(packet []byte) error {
	n, err := w.sink.WritePacket(packet)
	if err != nil {
		if n > 0 {
			return io.ErrShortWrite
		}
		return err
	}
	if n != len(packet) {
		return io.ErrShortWrite
	}
	return nil
}

func (w *Writer) discardConsumedPrefix(consumed int) {
	if consumed == 0 {
		return
	}
	remaining := len(w.sampleBuf) - consumed
	copy(w.sampleBuf, w.sampleBuf[consumed:])
	w.sampleBuf = w.sampleBuf[:remaining]
}

// decodePCMInto converts bytes to float32 PCM samples using caller-provided scratch.
func (w *Writer) decodePCMInto(dst []float32, data []byte) {
	switch w.format {
	case FormatFloat32LE:
		for i := range dst {
			bits := binary.LittleEndian.Uint32(data[i*4:])
			dst[i] = math.Float32frombits(bits)
		}
	case FormatInt16LE:
		for i := range dst {
			sample := int16(binary.LittleEndian.Uint16(data[i*2:]))
			dst[i] = float32(sample) / 32768.0
		}
	}
}

// Flush encodes any buffered samples.
// If samples don't fill a complete frame, they are zero-padded.
// Call Flush before closing the stream to ensure all audio is encoded.
func (w *Writer) Flush() error {
	if w.closed {
		return io.ErrClosedPipe
	}
	if len(w.sampleBuf) == 0 {
		return nil
	}

	// Zero-pad to complete frame using reusable scratch.
	clear(w.paddedBuf)
	copy(w.paddedBuf, w.sampleBuf)
	pcm := w.pcmScratch[:w.frameSamples]
	w.decodePCMInto(pcm, w.paddedBuf)

	// Encode the frame
	n, err := w.enc.Encode(pcm, w.packetBuf)
	if err != nil {
		return err
	}

	// If n > 0, send packet to sink
	if n > 0 {
		if err := w.writePacketToSink(w.packetBuf[:n]); err != nil {
			w.closed = true
			return err
		}
	}

	// Clear buffer
	w.sampleBuf = w.sampleBuf[:0]

	return nil
}

// Close flushes buffered samples and closes the underlying sink when supported.
//
// If the sink implements io.Closer, Close forwards to it after a successful
// flush. Close is idempotent.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	if err := w.Flush(); err != nil {
		return err
	}
	w.closed = true
	if closer, ok := w.sink.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// SetBitrate sets the target bitrate in bits per second.
// Valid range is 6000 to 510000 (6 kbps to 510 kbps).
func (w *Writer) SetBitrate(bitrate int) error {
	return w.enc.SetBitrate(bitrate)
}

// SetComplexity sets the encoder's computational complexity (0-10).
func (w *Writer) SetComplexity(complexity int) error {
	return w.enc.SetComplexity(complexity)
}

// SetFEC enables or disables in-band Forward Error Correction.
func (w *Writer) SetFEC(enabled bool) {
	w.enc.SetFEC(enabled)
}

// SetDTX enables or disables Discontinuous Transmission.
func (w *Writer) SetDTX(enabled bool) {
	w.enc.SetDTX(enabled)
}

// Reset clears buffers and encoder state for a new stream.
// It also clears the closed flag so the writer can be reused with a reusable sink.
func (w *Writer) Reset() {
	w.enc.Reset()
	w.sampleBuf = w.sampleBuf[:0]
	w.closed = false
}

// SampleRate returns the sample rate in Hz.
func (w *Writer) SampleRate() int {
	return w.enc.SampleRate()
}

// Channels returns the number of audio channels (1 or 2).
func (w *Writer) Channels() int {
	return w.enc.Channels()
}
