# --- build stage ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/docx2pdf ./cmd/docx2pdf

# --- runtime stage ---
# We bundle Noto Sans CJK so the binary works out of the box on Chinese docs.
# This is the only "non-pure-Go" piece in the image — the binary itself is
# CGO_ENABLED=0 and self-contained; the font is just a data file.
FROM alpine:3.20
RUN apk add --no-cache font-noto font-noto-cjk ca-certificates
COPY --from=build /out/docx2pdf /usr/local/bin/docx2pdf
# Default font paths inside the image; pass via -font / -font-fallback flags.
ENV DOCX2PDF_FONT=/usr/share/fonts/noto/NotoSans-Regular.ttf
ENV DOCX2PDF_FONT_CJK=/usr/share/fonts/noto/NotoSansCJK-Regular.ttc
ENTRYPOINT ["/usr/local/bin/docx2pdf"]
CMD ["-help"]
