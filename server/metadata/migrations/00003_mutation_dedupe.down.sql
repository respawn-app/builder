-- +goose Down

DROP INDEX mutation_dedupe_expires_idx;
DROP TABLE mutation_dedupe;
