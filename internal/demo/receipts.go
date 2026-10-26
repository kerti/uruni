package demo

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/receipt"
)

// A demo receipt is drawn at seed time: a paper-toned slip with a few lines of
// text, in the bitmap font the standard library's neighbour x/image already
// carries (no font file, no new dependency, no binary fixture in the repo). It
// then takes the same road a treasurer's photo does: receipt.Process, receipt.Store
// under the uploads volume, and a ledger.CreateReceipt row.

// receiptSlip is what is printed on one demo receipt.
type receiptSlip struct {
	shop  string
	lines []slipLine
	total int64
}

type slipLine struct {
	what   string
	amount int64
}

// slipScale is how much the 1x bitmap slip is enlarged, so the text reads on a
// phone screen like a thermal-printer receipt photographed close up.
const slipScale = 3

var (
	paper = color.RGBA{R: 0xF3, G: 0xEA, B: 0xD3, A: 0xFF}
	edge  = color.RGBA{R: 0xD9, G: 0xCC, B: 0xAA, A: 0xFF}
	ink   = color.RGBA{R: 0x3B, G: 0x36, B: 0x30, A: 0xFF}
)

// drawSlip renders the slip as a PNG.
func drawSlip(s receiptSlip) ([]byte, error) {
	const (
		width   = 240
		margin  = 12
		lineGap = 15
	)
	face := basicfont.Face7x13
	height := margin*2 + lineGap*(len(s.lines)+6)
	small := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(small, small.Bounds(), image.NewUniform(paper), image.Point{}, draw.Src)
	for x := 0; x < width; x++ { // a darker edge, like the paper's cut
		small.SetRGBA(x, 0, edge)
		small.SetRGBA(x, height-1, edge)
	}
	for y := 0; y < height; y++ {
		small.SetRGBA(0, y, edge)
		small.SetRGBA(width-1, y, edge)
	}

	drawer := &font.Drawer{Dst: small, Src: image.NewUniform(ink), Face: face}
	y := margin + 10
	text := func(x int, str string) {
		drawer.Dot = fixed.P(x, y)
		drawer.DrawString(str)
	}
	rule := func() {
		for x := margin; x < width-margin; x += 4 {
			small.SetRGBA(x, y-4, ink)
			small.SetRGBA(x+1, y-4, ink)
		}
		y += lineGap
	}
	right := func(str string) int { return width - margin - font.MeasureString(face, str).Round() }

	title := strings.ToUpper(s.shop)
	text((width-font.MeasureString(face, title).Round())/2, title)
	y += lineGap
	text(margin, "NOTA PEMBELIAN")
	y += lineGap
	rule()
	for _, l := range s.lines {
		text(margin, l.what)
		amount := slipAmount(l.amount)
		text(right(amount), amount)
		y += lineGap
	}
	rule()
	text(margin, "TOTAL")
	total := slipAmount(s.total)
	text(right(total), total)
	y += lineGap
	text(margin, "Terima kasih")

	big := image.NewRGBA(image.Rect(0, 0, width*slipScale, height*slipScale))
	draw.NearestNeighbor.Scale(big, big.Bounds(), small, small.Bounds(), draw.Src, nil)

	var buf bytes.Buffer
	if err := png.Encode(&buf, big); err != nil {
		return nil, fmt.Errorf("encoding the receipt image: %w", err)
	}
	return buf.Bytes(), nil
}

// slipAmount is money.FormatIDR without the no-break space, which the slip's
// ASCII bitmap font cannot draw: "Rp 285.000".
func slipAmount(n int64) string {
	return strings.ReplaceAll(money.FormatIDR(money.FromDB(n)), "\u00a0", " ")
}

// attachReceipt stores a drawn slip as the receipt of a posted transaction or a
// reimbursement claim, through the road the HTTP upload takes: process, write
// under the uploads volume, record the row. Exactly one of transactionID and
// reimbursementID is non-nil.
func attachReceipt(ctx context.Context, l *ledger.Ledger, fundID int64, uploadsDir string, transactionID, reimbursementID *int64, slip receiptSlip) error {
	raw, err := drawSlip(slip)
	if err != nil {
		return err
	}
	processed, err := receipt.Process(raw)
	if err != nil {
		return fmt.Errorf("processing the receipt image: %w", err)
	}
	name, err := receipt.Store(uploadsDir, processed)
	if err != nil {
		return fmt.Errorf("writing the receipt file: %w", err)
	}
	if _, err := l.CreateReceipt(ctx, ledger.CreateReceiptParams{
		FundID: fundID, TransactionID: transactionID, ReimbursementID: reimbursementID, Path: name,
	}); err != nil {
		return fmt.Errorf("recording the receipt: %w", err)
	}
	return nil
}
