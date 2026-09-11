package audio

// chunkBacklog is how many chunks the channel returned by StreamChunks can
// buffer before the producer goroutine blocks. At 100ms chunks this is
// ~10s of headroom, comfortably more than a single decode pass (hundreds
// of ms) should ever need -- see internal/stream, which consumes chunks
// from here while running ASR synchronously per chunk. Without this
// decoupling, a slow consumer blocking the same loop that reads from the
// capture device causes the device's own (much smaller) hardware buffer to
// overrun, silently dropping audio.
const chunkBacklog = 100

// Chunk is one buffer of captured samples, or a terminal error.
type Chunk struct {
	Samples []int16
	Err     error
}

// StreamChunks continuously reads chunkSamples-sized chunks from src on a
// dedicated goroutine and sends them on the returned channel, so a slow
// consumer never blocks the read loop and causes src to overrun. On a read
// error, one Chunk with Err set is sent and the channel is closed. The
// goroutine also exits, closing the channel, when stop is closed.
func StreamChunks(src Source, chunkSamples int, stop <-chan struct{}) <-chan Chunk {
	out := make(chan Chunk, chunkBacklog)
	go func() {
		defer close(out)
		for {
			select {
			case <-stop:
				return
			default:
			}

			buf := make([]int16, chunkSamples)
			n, err := src.ReadInt16(buf)
			if err != nil {
				select {
				case out <- Chunk{Err: err}:
				case <-stop:
				}
				return
			}

			select {
			case out <- Chunk{Samples: buf[:n]}:
			case <-stop:
				return
			}
		}
	}()
	return out
}
