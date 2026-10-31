// Package voice turns speech into text: it records the microphone with ffmpeg
// and asks an OpenAI-compatible endpoint to transcribe the audio.
package voice

import (
	"context"
	"encoding/binary"
	"math"
	"time"
)

// SampleRate is the rate the recorder captures at. It is also what Groq
// downsamples to, so nothing is lost on the way out.
const SampleRate = 16000

// Recorder captures the microphone.
type Recorder interface {
	// Record captures until ctx is cancelled or max elapses, and returns raw
	// 16-bit little-endian mono PCM at SampleRate.
	Record(ctx context.Context, max time.Duration) ([]byte, error)
}

// Transcriber turns PCM into text. prompt primes the recogniser with names and
// topic words, which is where speech recognition fails first on a learner.
type Transcriber interface {
	Transcribe(ctx context.Context, pcm []byte, lang, prompt string) (string, error)
}

// WAV wraps raw 16 kHz mono PCM in a WAV container, which is what the
// transcription endpoints expect.
func WAV(pcm []byte) []byte {
	const hdr = 44
	b := make([]byte, hdr+len(pcm))
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(pcm)))
	copy(b[8:], "WAVE")
	copy(b[12:], "fmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)           // fmt chunk size
	binary.LittleEndian.PutUint16(b[20:], 1)            // PCM
	binary.LittleEndian.PutUint16(b[22:], 1)            // mono
	binary.LittleEndian.PutUint32(b[24:], SampleRate)   // sample rate
	binary.LittleEndian.PutUint32(b[28:], SampleRate*2) // byte rate
	binary.LittleEndian.PutUint16(b[32:], 2)            // block align
	binary.LittleEndian.PutUint16(b[34:], 16)           // bits per sample
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm))) // data size
	copy(b[hdr:], pcm)
	return b
}

// minVoicedSamples is a fifth of a second at 16 kHz: below that there is no
// utterance to transcribe, only a keypress.
const minVoicedSamples = SampleRate / 5

// silentRMS is about -48 dBFS: louder than room noise, quieter than speech.
const silentRMS = 0.004

// rms is the root mean square of the PCM, in the 0..1 range.
func rms(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[2*i:]))) / 32768
		sum += v * v
	}
	return math.Sqrt(sum / float64(n))
}

// loud reports whether a chunk carries something louder than room noise. The
// VAD works on short blocks, where the "too short to be speech" rule of Silent
// does not apply.
func loud(pcm []byte) bool { return rms(pcm) >= silentRMS }

// Silent reports whether the PCM carries no usable speech. Without VAD this is
// the cheap guard that stops an accidental Enter from sending noise to the
// tutor and paying for a transcription.
func Silent(pcm []byte) bool {
	if len(pcm)/2 < minVoicedSamples {
		return true
	}
	return !loud(pcm)
}

// TrimSilence drops whole blocks of quiet at the edges, keeping one block of
// margin on each side so the first and last phoneme survive. Whisper invents
// text on long stretches of silence, so trimming is cheaper than trusting it.
func TrimSilence(pcm []byte) []byte {
	if len(pcm) <= vadBlock {
		return pcm
	}
	first, last := -1, -1
	for i := 0; i+vadBlock <= len(pcm); i += vadBlock {
		if loud(pcm[i : i+vadBlock]) {
			if first < 0 {
				first = i
			}
			last = i + vadBlock
		}
	}
	if first < 0 {
		return nil
	}
	start := first - vadBlock
	if start < 0 {
		start = 0
	}
	end := last + vadBlock
	if end > len(pcm) {
		end = len(pcm)
	}
	return pcm[start:end]
}
