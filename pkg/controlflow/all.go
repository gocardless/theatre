package controlflow

// All runs each operation in succession, returning an error and ceasing execution as soon
// as the error is non-nil.
func All(operations ...func() error) error {
	for _, operation := range operations {
		if err := operation(); err != nil {
			return err
		}
	}

	return nil
}
