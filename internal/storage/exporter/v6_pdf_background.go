package exporter

import (
	"bytes"
	"fmt"
	"io"
	"math"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// MergeV6AnnotationsWithPDF overlays the v6 ink pages (rendered as a
// single PDF by rmc-go) on top of the document's original pdf payload.
// inkToPage maps each ink pdf page number (1..K) to the destination
// page number inside the background pdf (1..N); ink pages mapped past
// the end of the background (or listed in extraInkPages) are extra
// notebook pages appended after the document.
func MergeV6AnnotationsWithPDF(backgroundPDF, inkPDF []byte, inkToPage map[int]int, output io.Writer) error {
	inkRS := bytes.NewReader(inkPDF)

	conf := model.NewDefaultConfiguration()

	inkDims, err := api.PageDims(inkRS, conf)
	if err != nil {
		return fmt.Errorf("ink pdf dims: %w", err)
	}
	bgDims, err := api.PageDims(bytes.NewReader(backgroundPDF), conf)
	if err != nil {
		return fmt.Errorf("background pdf dims: %w", err)
	}
	if len(inkDims) == 0 || len(bgDims) == 0 {
		return fmt.Errorf("no pages to merge")
	}

	// split the ink pages into overlay stamps and trailing extras
	wmMap := map[int]*model.Watermark{}
	var extraPages []int
	for inkPage := 1; inkPage <= len(inkDims); inkPage++ {
		dest, ok := inkToPage[inkPage]
		if !ok || dest > len(bgDims) {
			extraPages = append(extraPages, inkPage)
			continue
		}
		wm, err := api.PDFWatermarkForReadSeeker(inkRS, inkPage, "pos: c", true, false, types.POINTS)
		if err != nil {
			return fmt.Errorf("watermark page %d: %w", inkPage, err)
		}
		// uniform fit: scale the ink page so it fits inside the
		// background page, aspect ratio preserved, centered
		f := math.Min(
			bgDims[dest-1].Width/inkDims[inkPage-1].Width,
			bgDims[dest-1].Height/inkDims[inkPage-1].Height,
		)
		wm.Scale = f
		wm.ScaleAbs = true
		wm.Pos = types.Center
		wmMap[dest] = wm
	}

	var result []byte
	if len(wmMap) > 0 {
		var overlaid bytes.Buffer
		if err := api.AddWatermarksMap(bytes.NewReader(backgroundPDF), &overlaid, wmMap, conf); err != nil {
			return fmt.Errorf("overlay: %w", err)
		}
		result = overlaid.Bytes()
	} else {
		result = backgroundPDF
	}

	if len(extraPages) > 0 {
		first, last := extraPages[0], extraPages[len(extraPages)-1]
		var extras bytes.Buffer
		sel := []string{fmt.Sprintf("%d-%d", first, last)}
		if err := api.Collect(bytes.NewReader(inkPDF), &extras, sel, conf); err != nil {
			return fmt.Errorf("extra ink pages: %w", err)
		}
		rsc := []io.ReadSeeker{bytes.NewReader(result), bytes.NewReader(extras.Bytes())}
		return api.MergeRaw(rsc, output, false, conf)
	}

	_, err = io.Copy(output, bytes.NewReader(result))
	return err
}
