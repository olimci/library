package main

import (
    "context"
    "encoding/csv"
    "encoding/json"
    "fmt"
    "github.com/olimci/sched"
    "log/slog"
    "net/http"
    "os"
    "sync"
    "time"
)

const (
    baseFileName = "logs/occupancy.csv"
    url          = "https://apps.dur.ac.uk/study-spaces/library/bill-bryson/occupancy/display?json&affluence"
    interval     = 30 * time.Second
)

type LevelData struct {
    Free           int     `json:"free"`
    Total          int     `json:"total"`
    FreePercentage float64 `json:"freePercentage"`
    UsedPercentage float64 `json:"usedPercentage"`
}

type Response struct {
    Telepen   LevelData            `json:"telepen"`
    Affluence map[string]LevelData `json:"affluence"`
}

var lock sync.RWMutex

func main() {
    s := sched.New()

    if err := s.Start(context.Background()); err != nil {
        slog.Error("failed to start scheduler", "err", err)
        return
    }

    s.Add(sched.Every(getOccupancy, interval))

    // rotate every monday at 00:00:00
    s.Add(sched.Weekday(rotateLog, nil, 0, 0, 0, nil))

    s.Wait()
}

func getOccupancy(ctx context.Context) error {
    client := new(http.Client)
    req, err := http.NewRequest("GET", url, nil)
    if err != nil {
        slog.Error("failed to create request", "err", err)
        return err
    }
    req.Header.Set("User-Agent", "oli-bot/1.0 (+https://oli.mcinnes.cc)")

    resp, err := client.Do(req)
    if err != nil {
        slog.Error("failed to send request", "err", err)
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        slog.Error("bad status code", "code", resp.StatusCode)
        return fmt.Errorf("bad status code: %d", resp.StatusCode)
    }

    var data Response
    if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
        slog.Error("failed to decode response", "err", err)
        return err
    }

    lock.RLock()
    defer lock.RUnlock()

    return writeCSV(baseFileName, data)
}

func writeCSV(path string, data Response) error {
    fileExists := fileExists(path)

    file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
    if err != nil {
        slog.Error("failed to open CSV file", "err", err)
        return err
    }
    defer file.Close()

    writer := csv.NewWriter(file)
    defer writer.Flush()

    if !fileExists {
        header := []string{
            "timestamp",
            "telepen_free", "telepen_total", "telepen_free_pct", "telepen_used_pct",
        }
        for level := range data.Affluence {
            header = append(header,
                fmt.Sprintf("%s_free", level),
                fmt.Sprintf("%s_total", level),
                fmt.Sprintf("%s_free_pct", level),
                fmt.Sprintf("%s_used_pct", level),
            )
        }
        if err := writer.Write(header); err != nil {
            slog.Error("failed to write CSV header", "err", err)
            return err
        }
    }

    row := []string{
        time.Now().Format(time.RFC3339),
        fmt.Sprint(data.Telepen.Free),
        fmt.Sprint(data.Telepen.Total),
        fmt.Sprintf("%.1f", data.Telepen.FreePercentage),
        fmt.Sprintf("%.1f", data.Telepen.UsedPercentage),
    }
    for _, level := range []string{"Level1", "Level2e", "Level3e", "Level3nsw", "Level4e", "Level4nsw"} {
        lv := data.Affluence[level]
        row = append(row,
            fmt.Sprint(lv.Free),
            fmt.Sprint(lv.Total),
            fmt.Sprintf("%.1f", lv.FreePercentage),
            fmt.Sprintf("%.1f", lv.UsedPercentage),
        )
    }

    if err := writer.Write(row); err != nil {
        slog.Error("failed to write CSV row", "err", err)
        return err
    }

    slog.Info("logged occupancy data", "timestamp", row[0])
    return nil
}

func rotateLog(context.Context) error {
    lock.Lock()
    defer lock.Unlock()

    // use timestamp in filename e.g., occupancy_2025-05-19.csv
    suffix := time.Now().Format("2006-01-02")
    newName := fmt.Sprintf("occupancy_%s.csv", suffix)

    // skip if file doesn't exist
    if !fileExists(baseFileName) {
        slog.Info("no file to rotate")
        return nil
    }

    // avoid overwrite
    if fileExists(newName) {
        slog.Warn("rotation target already exists", "target", newName)
        return nil
    }

    err := os.Rename(baseFileName, newName)
    if err != nil {
        slog.Error("failed to rotate log", "err", err)
        return err
    }

    slog.Info("rotated log file", "new", newName)
    return nil
}

func fileExists(path string) bool {
    _, err := os.Stat(path)
    return err == nil
}
