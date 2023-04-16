package wallet

type Tx interface {
	Rollback() error
	Commit() error
}
