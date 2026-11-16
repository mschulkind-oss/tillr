"""Hold an uncommitted transaction until the parent kills this process."""
import sqlite3
import sys
import time

conn = sqlite3.connect(sys.argv[1])
if len(sys.argv) > 2:
    conn.execute("PRAGMA journal_mode=DELETE")
conn.execute("PRAGMA cache_size=1")
conn.execute("PRAGMA synchronous=FULL")
conn.execute("BEGIN IMMEDIATE")
# Force dirty pages out of the tiny cache: DELETE mode must leave a genuinely
# hot journal, not a journal whose header SQLite has never made durable.
conn.execute("INSERT INTO meta(key,value) VALUES ('killed-writer', zeroblob(1048576))")
print("ready", flush=True)
while True:
    time.sleep(60)
