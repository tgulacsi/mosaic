// Copyright 2016 Tamás Gulácsi
//
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

package main

import (
	"encoding/gob"
	"flag"
	"image"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/mjibson/go-dsp/fft"
	"github.com/pkg/errors"
)

const Width = 128

func main() {
	flagDB := flag.String("db", "mosaic.db", "DB file for thumbnails")
	flagOut := flag.String("o", "-", "output")
	flag.Parse()

	if err := Main(*flagOut, *flagDB, flag.Args()); err != nil {
		log.Fatal(err)
	}
}

func Main(outFn, dbFn string, files []string) error {
	out := os.Stdout
	if !(outFn == "" || outFn == "-") {
		var err error
		if out, err = os.Create(outFn); err != nil {
			return errors.Wrap(err, outFn)
		}
	}
	defer out.Close()

	thumbnails, err := prepareThumbnails(dbFn, files)
	if err != nil {
		return err
	}
	sortedThumbs := newSortedThumbnails(thumbnails, files)
	for i, t := range sortedThumbs {
		log.Printf("%d. %s %#v=%v", i, t.Name, t.FFT[0], R(t.FFT[0]))
	}

	target, err := imaging.Open(files[0])
	if err != nil {
		return errors.Wrap(err, files[0])
	}

	n := 3
	for n*n < len(files) {
		n++
	}
	log.Printf("Will use %d*%d=%d files", n, n, n*n)

	//b := target.Bounds()
	//W, H := b.Max.X-b.Min.X, b.Max.Y-b.Min.Y
	tgt := imaging.Resize(target, n*Width, n*Width, imaging.Lanczos)
	b := tgt.Bounds()
	for i := b.Min.Y; i < b.Max.Y; i += Width {
		for j := b.Min.X; j < b.Max.X; j += Width {
			found := sortedThumbs.FindImg(
				imaging.Crop(
					tgt,
					image.Rectangle{
						Min: image.Point{X: b.Min.X + i, Y: b.Min.Y + j},
						Max: image.Point{X: b.Min.X + i + Width, Y: b.Min.Y + j + Width},
					},
				),
			)
			log.Println(found)
		}
	}

	return out.Close()
}

func R(c complex128) float64 { return real(c)*real(c) + imag(c)*imag(c) }

func thumbLess(a, b [Width * Width]complex128) bool {
	for i, va := range a {
		vb := b[i]
		if ra, rb := R(va), R(vb); ra > rb {
			return false
		}
	}
	return true
}

type sortedThumbnails []Thumbnail

func newSortedThumbnails(thumbnails map[string]Thumbnail, files []string) sortedThumbnails {
	sorted := make([]Thumbnail, len(files))
	for i, fn := range files {
		sorted[i] = thumbnails[fn]
	}
	sort.Slice(
		sorted,
		func(i, j int) bool { return thumbLess(sorted[i].FFT, sorted[j].FFT) },
	)
	return sorted
}

func (s sortedThumbnails) FindImg(img image.Image) string {
	needle := imgFFT(img)
	log.Printf("needle[0]=%#v=%v", needle[0], R(needle[0]))
	i := sort.Search(len(s), func(i int) bool { return thumbLess(needle, s[i].FFT) })
	if i < 0 {
		i = 0
	} else if i >= len(s) {
		i = len(s) - 1
	}
	return s[i].Name
}

func prepareThumbnails(dbFn string, files []string) (map[string]Thumbnail, error) {
	thumbnails := make(map[string]Thumbnail, len(files))
	if dbFh, err := os.Open(dbFn); err == nil {
		gob.NewDecoder(dbFh).Decode(&thumbnails)
	}
	for i, fn := range files {
		fn, err := filepath.Abs(fn)
		if err != nil {
			log.Println(errors.Wrap(err, fn))
			continue
		}
		files[i] = fn
		fi, err := os.Stat(fn)
		if err != nil {
			log.Println(errors.Wrap(err, fn))
			continue
		}
		if old := thumbnails[fn]; old.Name == fi.Name() && old.ModTime.Equal(fi.ModTime()) {
			continue
		}
		thumb := Thumbnail{Name: fi.Name(), ModTime: fi.ModTime()}
		img, err := imaging.Open(fn)
		if err != nil {
			log.Println(errors.Wrap(err, fn))
			continue
		}
		thumb.FFT = imgFFT(img)
		thumbnails[fn] = thumb
	}

	dbFh, err := os.Create(dbFn)
	if err != nil {
		return thumbnails, errors.Wrap(err, dbFn)
	}
	err = gob.NewEncoder(dbFh).Encode(thumbnails)
	if closeErr := dbFh.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return thumbnails, errors.Wrap(err, dbFn)
}

type Thumbnail struct {
	Name    string
	ModTime time.Time
	FFT     [Width * Width]complex128
}

type backing struct {
	Array  [Width * Width]float64
	Matrix [][]float64
}

var backingPool = sync.Pool{New: func() interface{} {
	var b backing
	b.Matrix = make([][]float64, Width)
	for i := range b.Matrix {
		b.Matrix[i] = b.Array[i*Width : (i+1)*Width]
	}
	return &b
}}

func imgFFT(img image.Image) [Width * Width]complex128 {
	nrgba, _ := img.(*image.NRGBA)
	if nrgba == nil || img.ColorModel() != color.GrayModel {
		nrgba = imaging.Grayscale(img)
	}
	if b := nrgba.Bounds(); b.Max.X-b.Min.X > Width || b.Max.Y-b.Min.Y > Width {
		nrgba = imaging.Resize(nrgba, Width, Width, imaging.Lanczos)
	}

	b := backingPool.Get().(*backing)
	// TODO(tgulacsi): spiral from the center
	for i := 0; i < Width; i++ {
		for j := 0; j < Width; j++ {
			b.Array[i*Width+j] = float64(nrgba.Pix[nrgba.PixOffset(i, j)])
		}
	}
	mtx := fft.FFT2Real(b.Matrix)
	var carr [Width * Width]complex128
	for i, vv := range mtx {
		for j, v := range vv {
			carr[i*Width+j] = v
		}
	}
	return carr
}
