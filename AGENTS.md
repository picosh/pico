# Intro

This is a monorepo that contains a bunch of our cloud services leveraging golang's crypto/ssh library.  @README.md has more information about what they are and how they work.

# Commands

```bash
make fmt # format code
make lint # lint code
make test # test code
make build # build all go binaries
make check # runs all the commands above
```

Before telling me a task is complete, you **must** run `make check` before notifying me.

# Task management

We use a local tool called `bd` to manage tasks.  When using `bd`, work on a single task at a time.  Once you think you have finished a task, I **must** review the task before you can mark it complete.

To learn how to use `bd` run `bd quickstart`.

When I ask you to do something and that task is large enough that it should be broken into smaller tasks, you **must** use `bd`.
