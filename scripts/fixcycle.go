package main

import (
    "database/sql"
    "fmt"
    _ "github.com/mattn/go-sqlite3"
)

func main() {
    db, err := sql.Open("sqlite3", "/workspace/tillr.db")
    if err != nil { panic(err) }
    defer db.Close()
    _, err = db.Exec("UPDATE cycle_instances SET status='completed', updated_at=datetime('now') WHERE id=12")
    if err != nil { panic(err) }
    fmt.Println("cycle 12 completed")
}
