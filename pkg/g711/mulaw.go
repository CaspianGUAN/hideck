// Package g711 contains platform-independent G.711 sample conversion.
package g711

const (
	muLawBias = 0x84
	muLawClip = 32635
)

func DecodeSample(value byte) int16 {
	value = ^value
	magnitude := ((int(value&0x0f) << 3) + muLawBias) << ((value & 0x70) >> 4)
	if value&0x80 != 0 {
		return int16(muLawBias - magnitude)
	}
	return int16(magnitude - muLawBias)
}

func EncodeSample(sample int16) byte {
	value, sign := int(sample), byte(0)
	if value < 0 {
		sign, value = 0x80, -value
	}
	if value > muLawClip {
		value = muLawClip
	}
	value += muLawBias
	exponent := 7
	for mask := 0x4000; exponent > 0 && value&mask == 0; mask >>= 1 {
		exponent--
	}
	mantissa := (value >> (exponent + 3)) & 0x0f
	return ^(sign | byte(exponent<<4) | byte(mantissa))
}

func Decode(payload []byte) []int16 {
	pcm := make([]int16, len(payload))
	for i, value := range payload {
		pcm[i] = DecodeSample(value)
	}
	return pcm
}

func Encode(pcm []int16) []byte {
	payload := make([]byte, len(pcm))
	for i, value := range pcm {
		payload[i] = EncodeSample(value)
	}
	return payload
}
