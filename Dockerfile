# --- build stage ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/docx2pdf ./cmd/docx2pdf

# --- runtime stage ---
# Font choices are constrained by what gopdf can render:
#   - Noto Sans (regular Latin) is plain TrueType — works directly.
#   - Noto Sans CJK is OpenType/CFF, which gopdf can NOT render. We use
#     WenQuanYi Zen Hei instead (TrueType, covers Simplified+Traditional
#     Chinese, Japanese kana, Korean Hangul). It's a TTC; the runtime
#     extracts face 0 transparently — see internal/render/ttc.go.
# Everything else is pure-Go, CGO_ENABLED=0, so fonts are the only data
# files we need.
FROM alpine:3.23
RUN apk add --no-cache font-noto font-wqy-zenhei ca-certificates
COPY --from=build /out/docx2pdf /usr/local/bin/docx2pdf
# These env vars are consumed by the binary when Options.FontRegular /
# FontFallback are empty (see internal/render/sysfont.go). Either env
# vars or -font / -font-fallback flags work; explicit flags win.
ENV DOCX2PDF_FONT=/usr/share/fonts/noto/NotoSans-Regular.ttf
ENV DOCX2PDF_FONT_CJK=/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc
ENTRYPOINT ["/usr/local/bin/docx2pdf"]
CMD ["-help"]
