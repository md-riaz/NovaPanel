package exec

func Run(string, []string) {}
func bad()                 { Run("/bin/bash", []string{"-c", "true"}) }
