-- name: CreateTaskAndGetBacklog :one
WITH inserted_task AS (
    INSERT INTO tasks (type, value, state, creation_time, last_update_time)
    VALUES ($1, $2, $3, $4, EXTRACT(epoch FROM clock_timestamp() AT TIME ZONE 'UTC'))
    RETURNING id
)
SELECT
    (SELECT id FROM inserted_task) AS task_id,
    (SELECT COUNT(*) FROM tasks WHERE tasks.state = 'pending') AS backlog_count;

-- name: GetTaskByID :one
SELECT id, type, value, state, creation_time, last_update_time
FROM tasks
WHERE id = $1;

-- name: UpdateTaskToState :exec
UPDATE tasks
SET state = $2, last_update_time = clock_timestamp() AT TIME ZONE 'UTC'
WHERE id = $1;

-- name: GetTaskByIDUpdateState :one
WITH modified AS (
	UPDATE tasks
	SET state = $2
	WHERE id = $1
	RETURNING *
)
SELECT * FROM modified;
