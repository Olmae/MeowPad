package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
)

type mediaState struct {
	Title    string
	Progress byte
	Playing  bool
}

// Windows PowerShell provides the WinRT async projection used for media sessions.
const mediaScript = `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager, Windows.Media.Control, ContentType=WindowsRuntime]
$null = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties, Windows.Media.Control, ContentType=WindowsRuntime]
$asTask = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
    $_.Name -eq 'AsTask' -and $_.IsGenericMethodDefinition -and
    $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation#1'
} | Select-Object -First 1
function Await($operation, [type]$type) {
    $task = $asTask.MakeGenericMethod($type).Invoke($null, @($operation))
    if (!$task.Wait(1500)) { throw 'Media session timed out' }
    $task.Result
}
$manager = Await ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]::RequestAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager])
while ($true) {
    $result = @{Title=''; Progress=0; Playing=$false}
    try {
        $session = $manager.GetSessions() | Where-Object SourceAppUserModelId -Match 'spotify' | Select-Object -First 1
        if ($session) {
            $props = Await ($session.TryGetMediaPropertiesAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties])
            $result.Title = $props.Title
            $result.Playing = $session.GetPlaybackInfo().PlaybackStatus.ToString() -eq 'Playing'
            $timeline = $session.GetTimelineProperties()
            $duration = ($timeline.EndTime - $timeline.StartTime).TotalSeconds
            if ($duration -gt 0) {
                $position = ($timeline.Position - $timeline.StartTime).TotalSeconds
                if ($result.Playing) {
                    $position += [Math]::Max(0, ([DateTimeOffset]::Now - $timeline.LastUpdatedTime).TotalSeconds)
                }
                $result.Progress = [int][Math]::Round([Math]::Min(100, [Math]::Max(0, 100*$position/$duration)))
            }
        }
    } catch {}
    [Console]::WriteLine(($result | ConvertTo-Json -Compress))
    Start-Sleep -Milliseconds 850
}
`

func watchMedia(ctx context.Context, updates chan<- mediaState) {
	script := strings.ReplaceAll(mediaScript, "IAsyncOperation#1", "IAsyncOperation"+string(rune(96))+"1")
	executable := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	for ctx.Err() == nil {
		command := exec.CommandContext(ctx, executable, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		pipe, err := command.StdoutPipe()
		if err == nil && command.Start() == nil {
			scanner := bufio.NewScanner(pipe)
			for scanner.Scan() {
				var state mediaState
				if json.Unmarshal(scanner.Bytes(), &state) == nil {
					select {
					case updates <- state:
					case <-ctx.Done():
					}
				}
			}
			command.Wait()
		}
		select {
		case updates <- mediaState{}:
		case <-ctx.Done():
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func trackReport(state mediaState) [33]byte {
	flags := byte(0)
	if state.Playing {
		flags = 1
	}
	report := [33]byte{0, 0xAB, 'M', 'P', 2, 'T', flags, min(100, state.Progress)}
	copy(report[8:], oledText(state.Title))
	return report
}

func oledText(value string) string {
	const cyrillic = "абвгдежзийклмнопрстуфхцчшщъыьэюяё"
	translations := strings.Fields("a b v g d e zh z i y k l m n o p r s t u f kh ts ch sh shch ' y ' e yu ya yo")
	var text strings.Builder
	for _, letter := range value {
		switch {
		case letter >= 32 && letter <= 126:
			text.WriteRune(letter)
		default:
			index := strings.IndexRune(cyrillic, unicode.ToLower(letter))
			if index >= 0 {
				text.WriteString(translations[index/2])
			} else {
				text.WriteByte(' ')
			}
		}
	}
	return strings.Join(strings.Fields(text.String()), " ")
}
