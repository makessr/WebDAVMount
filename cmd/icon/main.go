package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"webdavmount/internal/branding"
)

func main() {
	if err := os.MkdirAll("build/windows", 0755); err != nil {
		panic(err)
	}
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	images := make([][]byte, len(sizes))
	for i, s := range sizes {
		images[i] = branding.PNG(s)
	}
	var out bytes.Buffer
	write := func(v any) {
		if err := binary.Write(&out, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	write(uint16(0))
	write(uint16(1))
	write(uint16(len(sizes)))
	offset := uint32(6 + 16*len(sizes))
	for i, s := range sizes {
		write(uint8(s % 256))
		write(uint8(s % 256))
		write(uint8(0))
		write(uint8(0))
		write(uint16(1))
		write(uint16(32))
		write(uint32(len(images[i])))
		write(offset)
		offset += uint32(len(images[i]))
	}
	for _, data := range images {
		out.Write(data)
	}
	if err := os.WriteFile("build/windows/app.ico", out.Bytes(), 0644); err != nil {
		panic(err)
	}
	if err := os.WriteFile("build/appicon.png", branding.PNG(256), 0644); err != nil {
		panic(err)
	}
	if err := os.MkdirAll("frontend/src/assets", 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile("frontend/src/assets/logo.png", branding.PNG(256), 0644); err != nil {
		panic(err)
	}
}
