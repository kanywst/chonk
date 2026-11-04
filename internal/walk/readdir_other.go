//go:build !darwin

package walk

func readDir(path string) ([]entry, error) { return readDirPortable(path) }
