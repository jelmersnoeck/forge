# Learnings - 2026-06-28 14:40

- git init does not create .git/hooks/ when the user has a custom init.templateDir that is empty or missing (e.g. ~/.git_template configured but nonexistent). Tests that write hook files must create the hooks directory first via os.MkdirAll — don't assume git init creates it.
