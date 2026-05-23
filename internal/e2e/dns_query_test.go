package e2e

import "strings"

func e2eDNSQuery(name string) []byte {
	packet := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(name, ".") {
		packet = append(packet, byte(len(label)))
		packet = append(packet, []byte(label)...)
	}
	packet = append(packet, 0x00, 0x00, 0x01, 0x00, 0x01)
	return packet
}
