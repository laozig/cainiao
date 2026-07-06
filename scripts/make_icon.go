package main

import (
	"bytes"
	"container/list"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

type point struct{ x, y int }

func isBackground(c color.Color) bool {
	r16, g16, b16, _ := c.RGBA()
	r, g, b := int(r16>>8), int(g16>>8), int(b16>>8)
	max := r
	if g > max {
		max = g
	}
	if b > max {
		max = b
	}
	min := r
	if g < min {
		min = g
	}
	if b < min {
		min = b
	}
	return r > 225 && g > 225 && b > 225 && max-min < 38
}

func transparentEdges(src image.Image) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)

	seen := make([]bool, w*h)
	q := list.New()
	push := func(x, y int) {
		if x >= 0 && y >= 0 && x < w && y < h {
			q.PushBack(point{x, y})
		}
	}
	for x := 0; x < w; x++ {
		push(x, 0)
		push(x, h-1)
	}
	for y := 0; y < h; y++ {
		push(0, y)
		push(w-1, y)
	}

	for q.Len() > 0 {
		e := q.Front()
		q.Remove(e)
		p := e.Value.(point)
		idx := p.y*w + p.x
		if seen[idx] {
			continue
		}
		seen[idx] = true
		if !isBackground(out.At(p.x, p.y)) {
			continue
		}
		o := out.PixOffset(p.x, p.y)
		out.Pix[o+3] = 0
		push(p.x+1, p.y)
		push(p.x-1, p.y)
		push(p.x, p.y+1)
		push(p.x, p.y-1)
	}
	return out
}

func resizeNearest(src image.Image, size int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		sy := b.Min.Y + y*b.Dy()/size
		for x := 0; x < size; x++ {
			sx := b.Min.X + x*b.Dx()/size
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func pngBytes(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeICO(path string, src image.Image) error {
	sizes := []int{16, 32, 48, 64, 128, 256}
	frames := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		b, err := pngBytes(resizeNearest(src, size))
		if err != nil {
			return err
		}
		frames = append(frames, b)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := binary.Write(f, binary.LittleEndian, uint16(0)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(len(frames))); err != nil {
		return err
	}
	offset := uint32(6 + len(frames)*16)
	for i, frame := range frames {
		sizeByte := byte(sizes[i])
		if sizes[i] == 256 {
			sizeByte = 0
		}
		entry := []byte{sizeByte, sizeByte, 0, 0}
		if _, err := f.Write(entry); err != nil {
			return err
		}
		for _, v := range []interface{}{uint16(1), uint16(32), uint32(len(frame)), offset} {
			if err := binary.Write(f, binary.LittleEndian, v); err != nil {
				return err
			}
		}
		offset += uint32(len(frame))
	}
	for _, frame := range frames {
		if _, err := f.Write(frame); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	in := filepath.Join("public", "image.png")
	outPNG := filepath.Join("public", "icon.png")
	outICO := "app.ico"

	f, err := os.Open(in)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	icon := transparentEdges(img)

	pf, err := os.Create(outPNG)
	if err != nil {
		panic(err)
	}
	if err := png.Encode(pf, icon); err != nil {
		pf.Close()
		panic(err)
	}
	pf.Close()

	if err := writeICO(outICO, icon); err != nil {
		panic(err)
	}
	fmt.Println(outPNG)
	fmt.Println(outICO)
}
