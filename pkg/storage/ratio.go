package storage

import (
	"fmt"
	"strconv"
	"strings"
)

type Ratio struct {
	Width  int
	Height int
}

func GetRatio(dimes string) (*Ratio, error) {
	if dimes == "" {
		return nil, nil
	}

	// bail if we detect imgproxy options
	if strings.Contains(dimes, ":") {
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
