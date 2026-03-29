# Quick Reference: Interactive Command Alternatives

When the Bash tool blocks an interactive command, here are the recommended alternatives:

## File Editing
❌ `vim file.txt`, `nano file.txt`, `emacs file.txt`  
✅ **Use Edit tool** with `file_path`, `old_string`, `new_string`  
✅ `cat file.txt` (to read)  
✅ `echo "content" > file.txt` (to write)

## File Viewing
❌ `less file.txt`, `more file.txt`  
✅ **Use Read tool** with `file_path`  
✅ `cat file.txt`  
✅ `tail -n 50 file.txt` (last 50 lines)  
✅ `head -n 50 file.txt` (first 50 lines)

## Interactive Shells & REPLs
❌ `python`, `python3`, `node`, `irb`  
✅ `python script.py` (run script)  
✅ `python -c "print('hello')"` (one-liner)  
✅ `node script.js` (run script)  
✅ `node -e "console.log('hello')"` (one-liner)

## Process Monitoring
❌ `top`, `htop`  
✅ `ps aux` (all processes)  
✅ `ps -eo pid,pcpu,pmem,comm --sort=-pcpu | head -20` (top 20 by CPU)  
✅ `ps -eo pid,pcpu,pmem,comm --sort=-pmem | head -20` (top 20 by memory)

## Package Management
❌ `npm init`  
✅ `npm init -y` (use defaults)  

❌ `yarn create`  
✅ `yarn create <package> --non-interactive`

❌ `rails generate model User`  
✅ `rails generate model User name:string --no-interaction`

## Database Clients
❌ `mysql` (interactive shell)  
✅ `mysql -e "SELECT * FROM users LIMIT 10"`  
✅ `mysql < script.sql`  

❌ `psql` (interactive shell)  
✅ `psql -c "SELECT * FROM users LIMIT 10"`  
✅ `psql -f script.sql`

## Container/Cluster Access
❌ `docker exec -it container bash`  
✅ `docker exec container ls -la` (run single command)  
✅ `docker logs container` (view logs)  

❌ `kubectl exec -it pod -- bash`  
✅ `kubectl exec pod -- ls -la` (run single command)  
✅ `kubectl logs pod` (view logs)

## Remote Access
❌ `ssh user@host` (interactive shell)  
✅ `ssh user@host "command"` (run remote command)  
✅ `ssh user@host "command" 2>&1` (capture stderr too)

## Git Operations
❌ `git commit` (opens editor)  
✅ `git commit -m "message"` (inline message)  

❌ `git rebase -i` (interactive rebase)  
✅ `git rebase main` (non-interactive rebase)  

❌ `git add -p` (interactive patch selection)  
✅ `git add file1 file2` (explicit files)  
✅ `git add .` (all changes)

## Terminal Multiplexers
❌ `tmux`, `screen`, `tmux attach`  
✅ Run commands in background: `command > output.log 2>&1 &`  
✅ Check background jobs: `jobs`  
✅ Get output: `cat output.log`

## Search & Selection
❌ `fzf` (interactive fuzzy finder)  
✅ **Use Grep tool** with pattern  
✅ `find . -name "*pattern*"`  
✅ `grep -r "pattern" .`

## General Pattern

**If a command needs user input:**
1. Look for non-interactive flags (`-y`, `--batch`, `--no-input`, etc.)
2. Use piping/redirection to provide input: `echo "input" | command`
3. Split into multiple non-interactive steps
4. Use appropriate Forge tools (Read, Write, Edit, Grep, etc.)

**Non-Interactive Flags to Try:**
- `-y`, `--yes`, `--assume-yes` (auto-confirm)
- `--batch`, `--non-interactive` (batch mode)
- `-f`, `--force` (skip prompts)
- `-c`, `-e` (code execution)
- `--no-input` (skip input prompts)
- `--defaults` (use default values)
