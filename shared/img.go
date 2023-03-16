package shared

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	gif "image/gif"
	jpeg "image/jpeg"
	png "image/png"
	"io"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/kolesa-team/go-webp/decoder"
	"github.com/kolesa-team/go-webp/encoder"
	"github.com/kolesa-team/go-webp/webp"
	"go.uber.org/zap"
)

type deviceType int

const (
	desktopDevice deviceType = iota
)

type ImgOptimizer struct {
	// Specify the compression factor for RGB channels between 0 and 100. The default is 75.
	// A small factor produces a smaller file with lower quality.
	// Best quality is achieved by using a value of 100.
	Quality float32
	*Ratio
	DeviceType deviceType
	Lossless   bool
}

type Ratio struct {
	Width  int
	Height int
}

var AlreadyWebPError = errors.New("image is already webp")
var IsSvgError = errors.New("image is an svg")

func GetImageForOptimization(r io.Reader, mimeType string) (image.Image, error) {
	switch mimeType {
	case "image/png":
		return png.Decode(r)
	case "image/jpeg":
		return jpeg.Decode(r)
	case "image/jpg":
		return jpeg.Decode(r)
	case "image/gif":
		return gif.Decode(r)
	case "image/webp":
		return nil, AlreadyWebPError
	}

	return nil, fmt.Errorf("(%s) not supported for optimization", mimeType)
}

func IsWebOptimized(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
		return true
	}

	return false
}

func CreateImgURL(linkify Linkify) func([]byte) []byte {
	return func(url []byte) []byte {
		if url[0] == '/' {
			name := SanitizeFileExt(string(url))
			nextURL := linkify.Create(name)
			return []byte(nextURL)
		} else if bytes.HasPrefix(url, []byte{'.', '/'}) {
			name := SanitizeFileExt(string(url[1:]))
			nextURL := linkify.Create(name)
			return []byte(nextURL)
		}
		return url
	}
}

func GetRatio(dimes string) (*Ratio, error) {
	if dimes == "" {
		return nil, nil
	}

	// dimes = x250 -- width is auto scaled and height is 250
	if strings.HasPrefix(dimes, "x") {
		height, err := strconv.Atoi(dimes[1:])
		if err != nil {
			return nil, err
		}
		return &Ratio{Width: 0, Height: height}, nil
	}

	// dimes = 250x -- width is 250 and height is auto scaled
	if strings.HasSuffix(dimes, "x") {
		width, err := strconv.Atoi(dimes[:len(dimes)-1])
		if err != nil {
			return nil, err
		}
		return &Ratio{Width: width, Height: 0}, nil
	}

	// dimes = 250x250
	res := strings.Split(dimes, "x")
	if len(res) != 2 {
		return nil, fmt.Errorf("(%s) must be in format (x200, 200x, or 200x200)", dimes)
	}

	ratio := &Ratio{}
	width, err := strconv.Atoi(res[0])
	if err != nil {
		return nil, err
	}
	ratio.Width = width

	height, err := strconv.Atoi(res[1])
	if err != nil {
		return nil, err
	}
	ratio.Height = height

	return ratio, nil
}

func (h *ImgOptimizer) DecodeWebp(r io.Reader) (image.Image, error) {
	opt := decoder.Options{}
	img, err := webp.Decode(r, &opt)
	if err != nil {
		return nil, err
	}

	if h.Width != 0 || h.Height != 0 {
		return imaging.Resize(
			img,
			h.Width,
			h.Height,
			imaging.CatmullRom,
		), nil
	}

	return img, nil
}

func (h *ImgOptimizer) EncodeWebp(writer io.Writer, img image.Image) error {
	var options *encoder.Options
	var err error
	if h.Lossless {
		options, err = encoder.NewLosslessEncoderOptions(
			encoder.PresetDefault,
			1, // only value that I could get to work
		)
	} else {
		options, err = encoder.NewLossyEncoderOptions(
			encoder.PresetDefault,
			h.Quality,
		)
	}
	if err != nil {
		return err
	}

	return webp.Encode(writer, img, options)
}

func (h *ImgOptimizer) Process(writer io.Writer, contents io.Reader) error {
	img, err := h.DecodeWebp(contents)
	if err != nil {
		return err
	}

	return h.EncodeWebp(writer, img)
}

func NewImgOptimizer(logger *zap.SugaredLogger, dimes string) *ImgOptimizer {
	opt := &ImgOptimizer{
		DeviceType: desktopDevice,
		Quality:    80,
		Ratio:      &Ratio{Width: 0, Height: 0},
	}

	ratio, err := GetRatio(dimes)
	if ratio != nil {
		opt.Ratio = ratio
	}

	if err != nil {
		logger.Error(err)
	}

	return opt
}
