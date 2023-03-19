// License: GPLv3 Copyright: 2023, Kovid Goyal, <kovid at kovidgoyal.net>

package diff

import (
	"fmt"
	"kitty/tools/tui/graphics"
	"kitty/tools/tui/loop"
)

var _ = fmt.Print

type ResultType int

const (
	COLLECTION ResultType = iota
	DIFF
	HIGHLIGHT
)

type ScrollPos struct {
	logical_line, screen_line int
}

type AsyncResult struct {
	err        error
	rtype      ResultType
	collection *Collection
	diff_map   map[string]*Patch
}

type Handler struct {
	async_results                                 chan AsyncResult
	left, right                                   string
	collection                                    *Collection
	diff_map                                      map[string]*Patch
	logical_lines                                 *LogicalLines
	lp                                            *loop.Loop
	current_context_count, original_context_count int
	added_count, removed_count                    int
	screen_size                                   struct{ rows, columns, num_lines int }
	scroll_pos                                    ScrollPos
}

func (self *Handler) calculate_statistics() {
	self.added_count, self.removed_count = self.collection.added_count, self.collection.removed_count
	for _, patch := range self.diff_map {
		self.added_count += patch.added_count
		self.removed_count += patch.removed_count
	}
}

var DebugPrintln func(...any)

func (self *Handler) initialize() {
	DebugPrintln = self.lp.DebugPrintln
	self.current_context_count = opts.Context
	if self.current_context_count < 0 {
		self.current_context_count = int(conf.Num_context_lines)
	}
	sz, _ := self.lp.ScreenSize()
	self.screen_size.rows = int(sz.HeightCells)
	self.screen_size.columns = int(sz.WidthCells)
	self.screen_size.num_lines = self.screen_size.rows - 1
	self.original_context_count = self.current_context_count
	self.lp.SetDefaultColor(loop.FOREGROUND, conf.Foreground)
	self.lp.SetDefaultColor(loop.CURSOR, conf.Foreground)
	self.lp.SetDefaultColor(loop.BACKGROUND, conf.Background)
	self.lp.SetDefaultColor(loop.SELECTION_BG, conf.Select_bg)
	if !conf.Select_fg.IsNull {
		self.lp.SetDefaultColor(loop.SELECTION_FG, conf.Select_fg.Color)
	}
	self.async_results = make(chan AsyncResult, 32)
	go func() {
		r := AsyncResult{}
		r.collection, r.err = create_collection(self.left, self.right)
		self.async_results <- r
		self.lp.WakeupMainThread()
	}()
	self.draw_screen()
}

func (self *Handler) generate_diff() {
	self.diff_map = nil
	jobs := make([]diff_job, 0, 32)
	self.collection.Apply(func(path, typ, changed_path string) error {
		if typ == "diff" {
			if is_path_text(path) && is_path_text(changed_path) {
				jobs = append(jobs, diff_job{path, changed_path})
			}
		}
		return nil
	})
	go func() {
		r := AsyncResult{rtype: DIFF}
		r.diff_map, r.err = diff(jobs, self.current_context_count)
		self.async_results <- r
		self.lp.WakeupMainThread()
	}()
}

func (self *Handler) on_wakeup() error {
	var r AsyncResult
	for {
		select {
		case r = <-self.async_results:
			if r.err != nil {
				return r.err
			}
			r.err = self.handle_async_result(r)
			if r.err != nil {
				return r.err
			}
		default:
			return nil
		}
	}
}

func (self *Handler) handle_async_result(r AsyncResult) error {
	switch r.rtype {
	case COLLECTION:
		self.collection = r.collection
		self.generate_diff()
	case DIFF:
		self.diff_map = r.diff_map
		self.calculate_statistics()
		err := self.render_diff()
		if err != nil {
			return err
		}
		self.scroll_pos = ScrollPos{}
		// TODO: restore_position uncomment and implement below
		// if self.restore_position != nil {
		// 	self.set_current_position(self.restore_position)
		// 	self.restore_position = nil
		// }
		self.draw_screen()
	case HIGHLIGHT:
	}
	return nil
}

func (self *Handler) on_resize(old_size, new_size loop.ScreenSize) error {
	self.screen_size.rows = int(new_size.HeightCells)
	self.screen_size.num_lines = self.screen_size.rows - 1
	self.screen_size.columns = int(new_size.WidthCells)
	if self.diff_map != nil && self.collection != nil {
		err := self.render_diff()
		if err != nil {
			return err
		}
	}
	self.draw_screen()
	return nil
}

func (self *Handler) render_diff() (err error) {
	self.logical_lines, err = render(self.collection, self.diff_map, self.screen_size.columns)
	if err != nil {
		return err
	}
	return nil
	// TODO: current search see python implementation
}

func (self *Handler) draw_screen() {
	self.lp.StartAtomicUpdate()
	defer self.lp.EndAtomicUpdate()
	g := (&graphics.GraphicsCommand{}).SetAction(graphics.GRT_action_delete).SetDelete(graphics.GRT_delete_visible)
	g.WriteWithPayloadToLoop(self.lp, nil)
	lp.MoveCursorTo(1, 1)
	lp.ClearToEndOfScreen()
	if self.logical_lines == nil || self.diff_map == nil || self.collection == nil {
		lp.Println(`Calculating diff, please wait...`)
		return
	}
	num_written := 0
	for i, line := range self.logical_lines.lines[self.scroll_pos.logical_line:] {
		if num_written >= self.screen_size.num_lines {
			break
		}
		screen_lines := line.screen_lines
		if i == 0 {
			screen_lines = screen_lines[self.scroll_pos.screen_line:]
		}
		for _, sl := range screen_lines {
			lp.QueueWriteString(sl)
			lp.MoveCursorVertically(1)
			lp.QueueWriteString("\r")
			num_written++
			if num_written >= self.screen_size.num_lines {
				break
			}
		}
	}

}