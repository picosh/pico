package shared

import (
	"fmt"
	"image"
	gif "image/gif"
	jpeg "image/jpeg"
	png "image/png"
	"io"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
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
	Quality   float32
	Optimized bool
	*Ratio
	DeviceType deviceType
}

func (h *ImgOptimizer) GetImage(r io.Reader, mimeType string) (image.Image, error) {
	switch mimeType {
	case "image/png":
		return png.Decode(r)
	case "image/jpeg":
		return jpeg.Decode(r)
	case "image/jpg":
		return jpeg.Decode(r)
	case "image/gif":
		return gif.Decode(r)
	}

	return nil, fmt.Errorf("(%s) not supported optimization", mimeType)
}

type Ratio struct {
	Width  int
	Height int
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

type SubImager interface {
	SubImage(r image.Rectangle) image.Image
}

func (h *ImgOptimizer) Resize(img image.Image) *image.NRGBA {
	return imaging.Resize(
		img,
		h.Width,
		h.Height,
		imaging.MitchellNetravali,
	)
}

func (h *ImgOptimizer) Process(contents io.Reader, writer io.Writer, mimeType string) error {
	if !h.Optimized {
		_, err := io.Copy(writer, contents)
		return err
	}

	img, err := h.GetImage(contents, mimeType)
	if err != nil {
		return err
	}

	nextImg := img
	if h.Height > 0 || h.Width > 0 {
		nextImg = h.Resize(img)
	}

	options, err := encoder.NewLossyEncoderOptions(
		encoder.PresetDefault,
		h.Quality,
	)
	if err != nil {
		return err
	}

	return webp.Encode(writer, nextImg, options)
}

func NewImgOptimizer(logger *zap.SugaredLogger, optimized bool, dimes string) *ImgOptimizer {
	opt := &ImgOptimizer{
		Optimized:  optimized,
		DeviceType: desktopDevice,
		Quality:    75,
	}

	ratio, err := GetRatio(dimes)
	if err == nil {
		opt.Ratio = ratio
	} else {
		logger.Error(err)
	}
	return opt
}
