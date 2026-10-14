package phone

import "github.com/yibaiba/hideck/pkg/g711"

// MuLawToPCM decodes one G.711 µ-law sample. Native VoLTE PCMU uses this path.
func MuLawToPCM(value byte) int16 {
	return muLawToPCM(value)
}

// PCMToMuLaw encodes one PCM sample as G.711 µ-law.
func PCMToMuLaw(sample int16) byte {
	return pcmToMuLaw(sample)
}

func muLawToPCM(value byte) int16 {
	return g711.DecodeSample(value)
}

func pcmToMuLaw(sample int16) byte {
	return g711.EncodeSample(sample)
}

func aLawToPCM(value byte) int16 {
	value ^= 0x55
	magnitude := int(value&0x0f) << 4
	exponent := int((value & 0x70) >> 4)
	if exponent == 0 {
		magnitude += 8
	} else {
		magnitude = (magnitude + 0x108) << (exponent - 1)
	}
	if value&0x80 == 0 {
		magnitude = -magnitude
	}
	return int16(magnitude)
}

func pcmToALaw(sample int16) byte {
	value := int(sample)
	sign := byte(0x80)
	if value < 0 {
		sign = 0
		value = -value - 1
	}
	if value > 32767 {
		value = 32767
	}
	var encoded byte
	if value < 256 {
		encoded = byte(value >> 4)
	} else {
		exponent := 1
		for threshold := 0x200; exponent < 7 && value >= threshold; threshold <<= 1 {
			exponent++
		}
		encoded = byte(exponent<<4) | byte((value>>(exponent+3))&0x0f)
	}
	return (encoded | sign) ^ 0x55
}

func transcodeG711(payload []byte, from, to string) {
	if from == to {
		return
	}
	for index, value := range payload {
		if from == "PCMA" {
			payload[index] = pcmToMuLaw(aLawToPCM(value))
		} else {
			payload[index] = pcmToALaw(muLawToPCM(value))
		}
	}
}
