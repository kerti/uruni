package http

import "html/template"

// reportPalette is the slice of web/src/index.css's :root tokens the public
// report uses, copied by hand (ADR-035): the page is one request with no
// asset path and no Vite coupling, so it cannot import the SPA's stylesheet.
// TestReportPaletteMatchesIndexCSS reads index.css and fails on any value
// that drifts from these, so the copy is kept honest by a test, not by a
// comment.
var reportPalette = map[string]string{
	"background":         "#f6f4ef",
	"foreground":         "#22323a",
	"card":               "#ffffff",
	"primary":            "#1f5d50",
	"primary-foreground": "#ffffff",
	"muted":              "#eeebe3",
	"muted-foreground":   "#67757c",
	"border":             "#e4e0d7",
	"input":              "#cfc8ba",
	"success":            "#2e7d5b",
	"success-soft":       "#e3f1e9",
	"attention":          "#c96c4a",
	"attention-soft":     "#f6e6dd",
}

// reportStyle is the page's one inline <style> block. System fonts, not the
// app's Plus Jakarta Sans: the page loads nothing but itself. Amounts use
// tabular figures (Design-System.md), and the layout is a single phone-width
// column, since the link mostly opens from a WhatsApp message.
//
// Colours come from reportPalette through CSS custom properties, so the
// rules below name tokens exactly as the SPA's Tailwind classes do.
func reportStyle() template.CSS {
	vars := ":root{"
	for _, name := range reportPaletteOrder {
		vars += "--" + name + ":" + reportPalette[name] + ";"
	}
	vars += "}"
	//nolint:gosec // every byte is a constant in this file: reportPalette, reportPaletteOrder and reportRules; no request data reaches it
	return template.CSS(vars + reportRules)
}

// reportPaletteOrder fixes the emitted order, so the page's bytes do not
// change between requests the way ranging over a map would make them.
var reportPaletteOrder = []string{
	"background", "foreground", "card", "primary", "primary-foreground",
	"muted", "muted-foreground", "border", "input",
	"success", "success-soft", "attention", "attention-soft",
}

const reportRules = `
*,*::before,*::after{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;background:var(--background);color:var(--foreground);
  font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
  font-size:16px;line-height:1.5}
main{max-width:40rem;margin:0 auto;padding:24px 16px 48px;display:flex;flex-direction:column;gap:24px}
h1{margin:0;font-size:24px;line-height:1.25;font-weight:700}
h2{margin:0 0 8px;font-size:14px;font-weight:600;color:var(--muted-foreground)}
p{margin:0}
.eyebrow{font-size:14px;font-weight:500;color:var(--muted-foreground)}
.muted{font-size:14px;color:var(--muted-foreground)}
.tabular{font-variant-numeric:tabular-nums}
.hero{background:var(--primary);color:var(--primary-foreground);border-radius:14px;
  padding:24px;text-align:center;display:flex;flex-direction:column;gap:4px}
.hero .label{font-size:16px;font-weight:500;opacity:.85}
.hero .amount{font-size:36px;font-weight:700;line-height:1.2}
.status{border-radius:8px;padding:12px 16px;font-size:15px}
.status.matched{background:var(--success-soft);color:var(--success)}
.status.discrepancy{background:var(--attention-soft);color:var(--attention)}
.status.never{background:var(--muted);color:var(--muted-foreground)}
.status .when{display:block;font-size:13px;opacity:.85;margin-top:2px}
ul.rows{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:8px}
ul.rows li{display:flex;justify-content:space-between;gap:12px;background:var(--card);
  border-radius:8px;padding:12px 16px;box-shadow:0 1px 2px rgb(34 50 58 / .06),0 4px 12px rgb(34 50 58 / .08)}
.negative{color:var(--attention)}
nav.months{display:flex;flex-direction:column;gap:12px}
nav.months .step{display:flex;justify-content:space-between;gap:12px;font-size:14px}
nav.months a{color:var(--primary);text-decoration:none;font-weight:500;padding:10px 0}
nav.months form{display:flex;gap:8px;align-items:center}
nav.months label{font-size:14px;color:var(--muted-foreground)}
select,button{font:inherit;font-size:16px;min-height:44px;border-radius:8px}
select{flex:1;border:1px solid var(--input);background:var(--card);color:var(--foreground);padding:0 12px}
button{border:0;background:var(--primary);color:var(--primary-foreground);padding:0 20px;font-weight:600}
.filters{display:flex;flex-direction:column;gap:12px;margin-bottom:16px}
.filters label{display:flex;flex-direction:column;gap:4px;font-size:14px;color:var(--muted-foreground)}
.totals{margin:0 0 16px;background:var(--card);border-radius:8px;padding:4px 16px;box-shadow:0 1px 2px rgb(34 50 58 / .06)}
.totals div{display:flex;justify-content:space-between;gap:12px;padding:8px 0}
.totals dt{color:var(--muted-foreground)}
.totals dd{margin:0;font-weight:600}
ul.txns li{align-items:flex-start}
.what{display:flex;flex-direction:column;min-width:0}
.label-line{font-weight:500;overflow-wrap:anywhere}
.amt{font-weight:600;white-space:nowrap}
.amt.in{color:var(--success)}
.amt.move{color:var(--muted-foreground)}
ul.dues li{flex-direction:column;gap:6px}
.dues .head{display:flex;justify-content:space-between;align-items:flex-start;gap:12px}
.dues .name{font-weight:500;overflow-wrap:anywhere;min-width:0}
.dues .tier{font-size:14px;color:var(--muted-foreground);overflow-wrap:anywhere}
.dues .amounts{display:flex;justify-content:space-between;gap:12px;font-size:14px;color:var(--muted-foreground)}
.dues .through{text-align:right;font-size:14px;color:var(--success)}
.badge{border-radius:999px;padding:4px 10px;font-size:14px;font-weight:500;white-space:nowrap;flex-shrink:0}
.sr-only{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
.badge.unpaid{background:var(--muted);color:var(--muted-foreground)}
.badge.partial{background:var(--attention-soft);color:var(--attention)}
.badge.paid{background:var(--success-soft);color:var(--success)}
.badge.paid_in_advance{background:var(--success-soft);color:var(--success);box-shadow:inset 0 0 0 1px var(--success)}
.envelopes{display:flex;flex-direction:column;gap:8px}
details.envelope{background:var(--card);border-radius:8px;padding:12px 16px;
  box-shadow:0 1px 2px rgb(34 50 58 / .06),0 4px 12px rgb(34 50 58 / .08)}
details.envelope[open]{display:flex;flex-direction:column;gap:12px}
details.envelope summary{cursor:pointer;display:flex;flex-direction:column;gap:6px;min-height:44px;justify-content:center}
details.envelope summary{list-style:none;position:relative}
details.envelope .env-facts{padding-right:28px}
details.envelope summary::-webkit-details-marker{display:none}
details.envelope summary::after{content:"";position:absolute;right:6px;bottom:10px;width:8px;height:8px;border-right:2px solid var(--muted-foreground);border-bottom:2px solid var(--muted-foreground);transform:rotate(45deg)}
details.envelope[open] summary::after{transform:rotate(-135deg);bottom:6px}
.env-head{display:flex;justify-content:space-between;align-items:flex-start;gap:12px}
.env-head .name,.people .name{font-weight:500;overflow-wrap:anywhere;min-width:0}
.env-facts{display:flex;justify-content:space-between;flex-wrap:wrap;gap:4px 12px;font-size:14px;color:var(--muted-foreground)}
.badge.open{background:var(--success-soft);color:var(--success)}
.badge.closed{background:var(--muted);color:var(--muted-foreground)}
details.envelope h3{margin:0;font-size:14px;font-weight:600;color:var(--muted-foreground)}
ul.people li{flex-direction:column;gap:2px;box-shadow:none;background:var(--background)}
.people .head{display:flex;justify-content:space-between;align-items:flex-start;gap:12px}
.people .amount{font-weight:500;white-space:nowrap;flex-shrink:0}
.state{font-size:14px;color:var(--muted-foreground)}
.state.sudah{color:var(--success)}
.empty{background:var(--card);border-radius:8px;padding:16px;color:var(--muted-foreground);text-align:center}
`
