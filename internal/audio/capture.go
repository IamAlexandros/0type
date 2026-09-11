package audio

/*
#cgo pkg-config: alsa
#include <alsa/asoundlib.h>
#include <errno.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// captureLatencyUS is the requested ALSA buffer latency. 100ms is generous
// enough to avoid overruns on ordinary hardware while staying well under the
// ~1-2s windows the streaming pipeline (Phase 3) will read at.
const captureLatencyUS = 100_000

// Capture is a blocking ALSA PCM capture stream. It implements Source.
type Capture struct {
	handle   *C.snd_pcm_t
	channels int
}

// OpenCapture opens the default ALSA capture device at the given sample
// rate and channel count, using signed 16-bit little-endian samples.
func OpenCapture(sampleRate, channels int) (*Capture, error) {
	var handle *C.snd_pcm_t
	cname := C.CString("default")
	defer C.free(unsafe.Pointer(cname))

	if rc := C.snd_pcm_open(&handle, cname, C.SND_PCM_STREAM_CAPTURE, 0); rc < 0 {
		return nil, fmt.Errorf("snd_pcm_open: %s", C.GoString(C.snd_strerror(rc)))
	}

	rc := C.snd_pcm_set_params(
		handle,
		C.SND_PCM_FORMAT_S16_LE,
		C.SND_PCM_ACCESS_RW_INTERLEAVED,
		C.uint(channels),
		C.uint(sampleRate),
		1, // allow soft resample
		C.uint(captureLatencyUS),
	)
	if rc < 0 {
		C.snd_pcm_close(handle)
		return nil, fmt.Errorf("snd_pcm_set_params: %s", C.GoString(C.snd_strerror(rc)))
	}

	return &Capture{handle: handle, channels: channels}, nil
}

// ReadInt16 blocks until len(buf)/channels frames have been captured,
// returning the number of interleaved samples written into buf. Buffer
// overruns (EPIPE) are recovered from transparently by re-preparing the
// stream and retrying.
func (c *Capture) ReadInt16(buf []int16) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	frames := C.snd_pcm_uframes_t(len(buf) / c.channels)
	for {
		n := C.snd_pcm_readi(c.handle, unsafe.Pointer(&buf[0]), frames)
		if n >= 0 {
			return int(n) * c.channels, nil
		}
		if int64(n) == int64(-C.EPIPE) {
			if rc := C.snd_pcm_prepare(c.handle); rc < 0 {
				return 0, fmt.Errorf("snd_pcm_prepare after overrun: %s", C.GoString(C.snd_strerror(rc)))
			}
			continue
		}
		return 0, fmt.Errorf("snd_pcm_readi: %s", C.GoString(C.snd_strerror(C.int(n))))
	}
}

// Close releases the underlying ALSA handle.
func (c *Capture) Close() error {
	C.snd_pcm_close(c.handle)
	return nil
}
