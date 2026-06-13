# Logo assets

Source SVGs for the menu bar gauges (`tacho swiftbar`). The rasterized
monochrome PNGs that the binary embeds live in
`internal/menubar/assets/{claude,codex}.png`.

To regenerate the PNGs after editing an SVG (requires librsvg —
`brew install librsvg`):

```sh
for n in claude codex; do
  rsvg-convert -w 256 -h 256 -b none assets/logos/$n.svg \
    -o internal/menubar/assets/$n.png
done
```

The runtime only reads the embedded PNGs, so no SVG rasterizer is needed to
build or run tachograph.

The Claude and OpenAI/Codex marks are trademarks of their respective owners;
they are bundled here for the personal instrument display only.
