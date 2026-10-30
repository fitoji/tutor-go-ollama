package speech

import "encoding/binary"

// SampleRate is the fixed output rate of the Phonon models.
const SampleRate = 24000

// WAV wraps raw 16-bit little-endian mono PCM (24 kHz) in a WAV container
// with correct sizes, so any player can open it.
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
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)))
	copy(b[hdr:], pcm)
	return b
}
