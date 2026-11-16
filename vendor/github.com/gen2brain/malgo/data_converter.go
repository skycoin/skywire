package malgo

/*
#include <stdlib.h>
#include "malgo.h"
*/
import "C"
import (
	"unsafe"
)

// DataConverterConfig type.
type DataConverterConfig struct {
	FormatIn               FormatType
	FormatOut              FormatType
	ChannelsIn             uint32
	ChannelsOut            uint32
	SampleRateIn           uint32
	SampleRateOut          uint32
	DitherMode             DitherMode
	ChannelMixMode         ChannelMixMode
	AllowDynamicSampleRate uint32
	Resampling             ResampleConfig

	// Unexposed: pChannelMapIn, pChannelMapOut, calculateLFEFromSpatialChannels, ppChannelWeights
}

// DefaultDataConverterConfig returns a default data converter config.
func DefaultDataConverterConfig(formatIn, formatOut FormatType, channelsIn, channelsOut, sampleRateIn, sampleRateOut uint32) DataConverterConfig {
	config := C.ma_data_converter_config_init(C.ma_format(formatIn), C.ma_format(formatOut),
		C.ma_uint32(channelsIn), C.ma_uint32(channelsOut), C.ma_uint32(sampleRateIn), C.ma_uint32(sampleRateOut))

	var c DataConverterConfig
	c.FormatIn = FormatType(config.formatIn)
	c.FormatOut = FormatType(config.formatOut)
	c.ChannelsIn = uint32(config.channelsIn)
	c.ChannelsOut = uint32(config.channelsOut)
	c.SampleRateIn = uint32(config.sampleRateIn)
	c.SampleRateOut = uint32(config.sampleRateOut)
	c.DitherMode = DitherMode(config.ditherMode)
	c.ChannelMixMode = ChannelMixMode(config.channelMixMode)
	c.AllowDynamicSampleRate = uint32(config.allowDynamicSampleRate)
	c.Resampling.Algorithm = ResampleAlgorithm(config.resampling.algorithm)
	c.Resampling.Linear.LpfOrder = uint32(config.resampling.linear.lpfOrder)

	return c
}

func (c *DataConverterConfig) toC() C.ma_data_converter_config {
	config := C.ma_data_converter_config_init_default()
	config.formatIn = C.ma_format(c.FormatIn)
	config.formatOut = C.ma_format(c.FormatOut)
	config.channelsIn = C.ma_uint32(c.ChannelsIn)
	config.channelsOut = C.ma_uint32(c.ChannelsOut)
	config.sampleRateIn = C.ma_uint32(c.SampleRateIn)
	config.sampleRateOut = C.ma_uint32(c.SampleRateOut)
	config.ditherMode = C.ma_dither_mode(c.DitherMode)
	config.channelMixMode = C.ma_channel_mix_mode(c.ChannelMixMode)
	config.allowDynamicSampleRate = C.ma_bool32(c.AllowDynamicSampleRate)
	config.resampling.algorithm = C.ma_resample_algorithm(c.Resampling.Algorithm)
	config.resampling.linear.lpfOrder = C.ma_uint32(c.Resampling.Linear.LpfOrder)

	return config
}

// DataConverter converts sample format, channel count and sample rate in one step.
type DataConverter struct {
	ptr          *C.ma_data_converter
	frameSizeIn  int
	frameSizeOut int
}

// InitDataConverter initializes a data converter. It has to be released with Uninit.
func InitDataConverter(config DataConverterConfig) (*DataConverter, error) {
	configC := config.toC()

	ptr := (*C.ma_data_converter)(C.malloc(C.sizeof_ma_data_converter))
	result := C.ma_data_converter_init(&configC, nil, ptr)
	if result != C.MA_SUCCESS {
		C.free(unsafe.Pointer(ptr))
		return nil, errorFromResult(result)
	}

	return &DataConverter{
		ptr:          ptr,
		frameSizeIn:  FrameSizeInBytes(config.FormatIn, config.ChannelsIn),
		frameSizeOut: FrameSizeInBytes(config.FormatOut, config.ChannelsOut),
	}, nil
}

// Uninit releases the data converter.
func (c *DataConverter) Uninit() {
	if c.ptr == nil {
		return
	}
	C.ma_data_converter_uninit(c.ptr, nil)
	C.free(unsafe.Pointer(c.ptr))
	c.ptr = nil
}

// ProcessFrames converts interleaved frames from in to out and returns the number of frames read and written.
func (c *DataConverter) ProcessFrames(in, out []byte) (uint64, uint64, error) {
	var pIn, pOut unsafe.Pointer
	if len(in) > 0 {
		pIn = unsafe.Pointer(&in[0])
	}
	if len(out) > 0 {
		pOut = unsafe.Pointer(&out[0])
	}

	frameCountIn := C.ma_uint64(len(in) / c.frameSizeIn)
	frameCountOut := C.ma_uint64(len(out) / c.frameSizeOut)

	result := C.ma_data_converter_process_pcm_frames(c.ptr, pIn, &frameCountIn, pOut, &frameCountOut)
	if result != C.MA_SUCCESS {
		return 0, 0, errorFromResult(result)
	}

	return uint64(frameCountIn), uint64(frameCountOut), nil
}

// SetRate changes the sample rates, the converter must be initialized with AllowDynamicSampleRate.
func (c *DataConverter) SetRate(sampleRateIn, sampleRateOut uint32) error {
	return errorFromResult(C.ma_data_converter_set_rate(c.ptr, C.ma_uint32(sampleRateIn), C.ma_uint32(sampleRateOut)))
}

// RequiredInputFrameCount returns the number of input frames needed to produce outputFrameCount frames.
func (c *DataConverter) RequiredInputFrameCount(outputFrameCount uint64) (uint64, error) {
	var count C.ma_uint64
	result := C.ma_data_converter_get_required_input_frame_count(c.ptr, C.ma_uint64(outputFrameCount), &count)
	if result != C.MA_SUCCESS {
		return 0, errorFromResult(result)
	}

	return uint64(count), nil
}

// ExpectedOutputFrameCount returns the number of output frames produced from inputFrameCount frames.
func (c *DataConverter) ExpectedOutputFrameCount(inputFrameCount uint64) (uint64, error) {
	var count C.ma_uint64
	result := C.ma_data_converter_get_expected_output_frame_count(c.ptr, C.ma_uint64(inputFrameCount), &count)
	if result != C.MA_SUCCESS {
		return 0, errorFromResult(result)
	}

	return uint64(count), nil
}

// InputLatency returns the input latency in frames.
func (c *DataConverter) InputLatency() uint64 {
	return uint64(C.ma_data_converter_get_input_latency(c.ptr))
}

// OutputLatency returns the output latency in frames.
func (c *DataConverter) OutputLatency() uint64 {
	return uint64(C.ma_data_converter_get_output_latency(c.ptr))
}

// Reset clears the internal state of the data converter.
func (c *DataConverter) Reset() error {
	return errorFromResult(C.ma_data_converter_reset(c.ptr))
}
