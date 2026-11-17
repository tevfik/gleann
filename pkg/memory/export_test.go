package memory

import "database/sql"

// ReaderForTest exposes the reader pool to the external memory_test package.
func (s *Store) ReaderForTest() *sql.DB { return s.reader }
