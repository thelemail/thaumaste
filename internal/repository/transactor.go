package repository

import "context"

type Transactor interface {
	WithTx(ctx context.Context, fn func(context.Context) error) error
}
