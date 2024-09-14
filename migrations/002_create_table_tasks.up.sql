CREATE TABLE IF NOT EXISTS tasks (
    id SERIAL PRIMARY KEY,
    type INT NOT NULL CHECK (type >= 0 AND type <= 9),
    value INT NOT NULL CHECK (value >= 0 AND value <= 99),
    state task_state NOT NULL DEFAULT 'pending',
    creation_time DOUBLE PRECISION NOT NULL,
    last_update_time DOUBLE PRECISION NOT NULL
);

