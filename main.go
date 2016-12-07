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
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/disintegration/imaging"
	"github.com/pkg/errors"
)

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
	sortedThumbs := sortThumbs(thumbnails)

	pattern, err := imaging.Open(files[0])
	if err != nil {
		return errors.Wrap(err, files[0])
	}

	mul := 3
	b := pattern.Bounds()
	W, H := b.Max.X-b.Min.X, b.Max.Y-b.Min.Y
	for len(thumbnails)*mul < W || len(thumbnails)*mul < H {
		mul++
	}
	log.Println(mul)
	pat := imaging.Resize(pattern, W/mul, H/mul, imaging.Lanczos)

	b = pat.Bounds()
	for i := b.Min.Y; i < b.Max.Y; i += 3 {
		for j := b.Min.X; j < b.Max.X; j += 3 {
			found := find(sortedThumbs, imaging.Crop(pat, image.Rectangle{Min: image.Point{X: b.Min.X + i, Y: b.Min.Y + j}, Max: image.Point{X: b.Min.X + i + 3, Y: b.Min.Y + j + 3}}))
		}
	}

	return out.Close()
}

func thumbLess(a, b *image.NRGBA) bool {
	for _, i := range []int{5,
		2, 6, 8, 4,
		1, 3, 7, 9,
	} {
		return a.Pix[(i-1)*4:i*4] < b.Pix[(i-1)*4:i*4]
	}
}
func sortThumbs(thumbnails map[string]Thumbnail) map[[9]uint8]string {
	sorted := make(map[[9]uint8]string, len(thumbnails))
	return sorted
}

func prepareThumbnails(dbFn string, files []string) (map[string]Thumbnail, error) {
	thumbnails := make(map[string]Thumbnail, len(files))
	if dbFh, err := os.Open(dbFn); err == nil {
		gob.NewDecoder(dbFh).Decode(&thumbnails)
	}
	for _, fn := range files {
		fn, err := filepath.Abs(fn)
		if err != nil {
			log.Println(errors.Wrap(err, fn))
			continue
		}
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
		thumb.NRGBA = imaging.Resize(img, 3, 3, imaging.Linear)
		thumbnails[fn] = thumb
	}
	log.Printf("%#v", thumbnails)

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
	*image.NRGBA
}
