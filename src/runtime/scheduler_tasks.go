// +build scheduler.tasks

package runtime

import "unsafe"

const stackSize = 1024

// Stack canary, to detect a stack overflow. The number is a random number
// generated by random.org. The bit fiddling dance is necessary because
// otherwise Go wouldn't allow the cast to a smaller integer size.
const stackCanary = uintptr(uint64(0x670c1333b83bf575) & uint64(^uintptr(0)))

var (
	schedulerState = task{canary: stackCanary}
	currentTask    *task // currently running goroutine, or nil
)

// This type points to the bottom of the goroutine stack and contains some state
// that must be kept with the task. The last field is a canary, which is
// necessary to make sure that no stack overflow occured when switching tasks.
type task struct {
	// The order of fields in this structs must be kept in sync with assembly!
	calleeSavedRegs
	sp uintptr
	pc uintptr
	taskState
	canary uintptr // used to detect stack overflows
}

// getCoroutine returns the currently executing goroutine. It is used as an
// intrinsic when compiling channel operations, but is not necessary with the
// task-based scheduler.
func getCoroutine() *task {
	return currentTask
}

// state is a small helper that returns the task state, and is provided for
// compatibility with the coroutine implementation.
//go:inline
func (t *task) state() *taskState {
	return &t.taskState
}

// resume is a small helper that resumes this task until this task switches back
// to the scheduler.
func (t *task) resume() {
	currentTask = t
	swapTask(&schedulerState, t)
	currentTask = nil
}

// swapTask saves the current state to oldTask (which must contain the current
// task state) and switches to newTask. Note that this function usually does
// return, when another task (perhaps newTask) switches back to the current
// task.
//
// As an additional protection, before switching tasks, it checks whether this
// goroutine has overflowed the stack.
func swapTask(oldTask, newTask *task) {
	if oldTask.canary != stackCanary {
		runtimePanic("goroutine stack overflow")
	}
	swapTaskLower(oldTask, newTask)
}

//go:linkname swapTaskLower tinygo_swapTask
func swapTaskLower(oldTask, newTask *task)

// Goexit terminates the currently running goroutine. No other goroutines are affected.
//
// Unlike the main Go implementation, no deffered calls will be run.
//export runtime.Goexit
func Goexit() {
	// Swap without rescheduling first, effectively exiting the goroutine.
	swapTask(currentTask, &schedulerState)
}

// startTask is a small wrapper function that sets up the first (and only)
// argument to the new goroutine and makes sure it is exited when the goroutine
// finishes.
//go:extern tinygo_startTask
var startTask [0]uint8

// startGoroutine starts a new goroutine with the given function pointer and
// argument. It creates a new goroutine stack, prepares it for execution, and
// adds it to the runqueue.
func startGoroutine(fn, args uintptr) {
	stack := alloc(stackSize)
	t := (*task)(stack)
	t.sp = uintptr(stack) + stackSize
	t.pc = uintptr(unsafe.Pointer(&startTask))
	t.prepareStartTask(fn, args)
	t.canary = stackCanary
	scheduleLogTask("  start goroutine:", t)
	runqueuePushBack(t)
}

//go:linkname sleep time.Sleep
func sleep(d int64) {
	sleepTask(currentTask, d)
	swapTask(currentTask, &schedulerState)
}

// deadlock is called when a goroutine cannot proceed any more, but is in theory
// not exited (so deferred calls won't run). This can happen for example in code
// like this, that blocks forever:
//
//     select{}
func deadlock() {
	Goexit()
}

// reactivateParent reactivates the parent goroutine. It is a no-op for the task
// based scheduler.
func reactivateParent(t *task) {
	// Nothing to do here, tasks don't stop automatically.
}

// chanYield exits the current goroutine. Used in the channel implementation, to
// suspend the current goroutine until it is reactivated by a channel operation
// of a different goroutine.
func chanYield() {
	Goexit()
}
