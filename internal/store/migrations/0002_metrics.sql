CREATE TABLE metrics_minute (
    ts INTEGER PRIMARY KEY,          -- unix, início do minuto
    cpu_percent REAL NOT NULL,
    mem_used INTEGER NOT NULL, mem_total INTEGER NOT NULL,
    load1 REAL NOT NULL,
    net_rx REAL NOT NULL, net_tx REAL NOT NULL,   -- bytes/s
    disk_used INTEGER NOT NULL, disk_total INTEGER NOT NULL  -- soma das montagens
);
