// Command icon writes Skilli's application icon as a 1024×1024 PNG.
//
//	go run ./scripts/icon resources/icon.png
//
// The release build runs it to give mygo build the icon it embeds in
// skilli-gui.exe, so no generated image is kept in the repository.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/otnc/Skilli/internal/icon"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "使い方: go run ./scripts/icon <出力する PNG のパス>")
		os.Exit(2)
	}
	data, err := icon.PNG(1024, false)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(os.Args[1]), 0o755)
	}
	if err == nil {
		err = os.WriteFile(os.Args[1], data, 0o644)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "アイコンを書き出せませんでした: %v\n", err)
		os.Exit(1)
	}
}
