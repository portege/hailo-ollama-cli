#!/usr/bin/env bash

# Exit on any error
set -e

# Target directory
DIST_DIR="dist"
mkdir -p "$DIST_DIR"

echo "Building Hailo Ollama CLI binaries..."

# Compile targets
TARGETS=(
  "linux/arm64"
  "linux/amd64"
  "darwin/arm64"
  "darwin/amd64"
)

for target in "${TARGETS[@]}"; do
  # Split the target into OS and ARCH
  IFS="/" read -r -a parts <<< "$target"
  goos="${parts[0]}"
  goarch="${parts[1]}"

  output_name="${DIST_DIR}/hailo-ollama-${goos}-${goarch}"
  if [ "$goos" = "windows" ]; then
    output_name="${output_name}.exe"
  fi

  echo "  -> Building for ${goos}/${goarch}..."
  GOOS="$goos" GOARCH="$goarch" go build -ldflags="-s -w" -o "$output_name" .
  chmod +x "$output_name"
done

echo "Build successful! Binaries created in the '${DIST_DIR}/' directory:"
ls -lh "$DIST_DIR"
