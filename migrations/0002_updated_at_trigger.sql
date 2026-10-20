-- +goose Up
-- ---------------------------------------------------------------------------
-- Let the application keep its own updated_at value.
--
-- Migration 0001 created a trigger that stamped every UPDATE with now().
-- Because the trigger runs BEFORE UPDATE, it discarded the timestamp the Go
-- service had already written, so the response of a PATCH and a later GET of
-- the same task reported two different updated_at values.
--
-- The replacement only stamps the row when the writer left updated_at alone.
-- The application always sends its own value, so that value survives. A
-- manual UPDATE that does not mention updated_at still gets now().
-- ---------------------------------------------------------------------------

-- goose splits migrations on semicolons. StatementBegin/StatementEnd tells it
-- to treat the whole dollar-quoted function body as one statement.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskflow.set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    -- NEW.updated_at equals OLD.updated_at when the writer did not supply a
    -- new value, which is exactly the case this trigger exists for.
    IF NEW.updated_at = OLD.updated_at THEN
        NEW.updated_at = now();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- The trigger itself does not change; only the function it calls does.
DROP TRIGGER IF EXISTS tasks_set_updated_at ON taskflow.tasks;
CREATE TRIGGER tasks_set_updated_at
    BEFORE UPDATE ON taskflow.tasks
    FOR EACH ROW
    EXECUTE FUNCTION taskflow.set_updated_at();

-- +goose Down
-- Restore the previous behaviour: every update is stamped with now().
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskflow.set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS tasks_set_updated_at ON taskflow.tasks;
CREATE TRIGGER tasks_set_updated_at
    BEFORE UPDATE ON taskflow.tasks
    FOR EACH ROW
    EXECUTE FUNCTION taskflow.set_updated_at();
