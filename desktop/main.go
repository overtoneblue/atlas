package main

import (
	"embed"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails embeds the built frontend into the binary. Anything in
// frontend/dist lands here and is served to the webview (desktop) or over
// HTTP (server mode).
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	openSession := flag.String("open", "", "session id to open at startup (dev/testing)")
	flag.Parse()

	data := NewDataService()
	data.SetInitialSession(*openSession)

	app := application.New(application.Options{
		Name:        "atlas-desktop",
		Description: "Atlas — workstream client for Hermes",
		Services: []application.Service{
			application.NewService(data),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Atlas",
		Width:  1500,
		Height: 950,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(13, 11, 10),
		URL:              "/",
		Linux: application.LinuxWindow{
			// WebKitGTK rendering policy. Software rendering is the safe
			// default: it is what headless previews run on (llvmpipe) and it
			// dodges the driver white-screen class of bugs the Wails default
			// already guards against. Opt into hardware acceleration
			// per-machine with ATLAS_GPU=ondemand|always.
			WebviewGpuPolicy: gpuPolicy(),
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// gpuPolicy maps ATLAS_GPU to the WebKitGTK hardware-acceleration policy.
// Unset or unrecognized values stay on software rendering.
func gpuPolicy() application.WebviewGpuPolicy {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ATLAS_GPU"))) {
	case "always":
		return application.WebviewGpuPolicyAlways
	case "ondemand", "on_demand", "on-demand":
		return application.WebviewGpuPolicyOnDemand
	default:
		return application.WebviewGpuPolicyNever
	}
}
