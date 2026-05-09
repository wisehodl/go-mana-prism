package prism

func bufferedPipe[T any](input <-chan T, output chan<- T) {
	var buffer []T

	for {
		var outOrNil chan<- T
		var next T

		// toggle send channel
		if len(buffer) > 0 {
			outOrNil = output
			next = buffer[0]
		} else if input == nil {
			// input closed
			return
		}

		select {
		case item, ok := <-input:
			if !ok {
				// input is closed, set input nil
				input = nil
				continue
			}
			buffer = append(buffer, item)
		case outOrNil <- next:
			// discard element, set to zero to free memory
			var zero T
			buffer[0] = zero
			buffer = buffer[1:]
		}
	}
}
