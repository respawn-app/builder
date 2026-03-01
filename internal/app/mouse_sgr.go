package app

func stripMouseSGRRunes(input []rune) ([]rune, bool) {
	if len(input) == 0 {
		return input, false
	}
	output := make([]rune, 0, len(input))
	removedAny := false
	for index := 0; index < len(input); {
		if next, ok := consumeMouseSGRRunes(input, index); ok {
			removedAny = true
			index = next
			continue
		}
		output = append(output, input[index])
		index++
	}
	return output, removedAny
}

func stripMouseSGRRunesWithCursor(input []rune, cursor int) ([]rune, int, bool) {
	if len(input) == 0 {
		return input, clampCursor(cursor, 0), false
	}
	clampedCursor := clampCursor(cursor, len(input))
	output := make([]rune, 0, len(input))
	removedAny := false
	adjustedCursor := clampedCursor
	for index := 0; index < len(input); {
		if next, ok := consumeMouseSGRRunes(input, index); ok {
			removedAny = true
			if next <= clampedCursor {
				adjustedCursor -= next - index
			} else if index < clampedCursor {
				adjustedCursor = index
			}
			index = next
			continue
		}
		output = append(output, input[index])
		index++
	}
	if adjustedCursor < 0 {
		adjustedCursor = 0
	}
	if adjustedCursor > len(output) {
		adjustedCursor = len(output)
	}
	return output, adjustedCursor, removedAny
}

func consumeMouseSGRRunes(input []rune, start int) (int, bool) {
	if start < 0 || start >= len(input) {
		return 0, false
	}
	position := start
	if input[position] == '\x1b' {
		if position+2 >= len(input) || input[position+1] != '[' || input[position+2] != '<' {
			return 0, false
		}
		position += 3
	} else {
		if position+1 >= len(input) || input[position] != '[' || input[position+1] != '<' {
			return 0, false
		}
		position += 2
	}
	for segment := 0; segment < 3; segment++ {
		digits := 0
		for position < len(input) && input[position] >= '0' && input[position] <= '9' {
			position++
			digits++
		}
		if digits == 0 {
			return 0, false
		}
		if segment < 2 {
			if position >= len(input) || input[position] != ';' {
				return 0, false
			}
			position++
		}
	}
	if position >= len(input) {
		return 0, false
	}
	if input[position] != 'M' && input[position] != 'm' {
		return 0, false
	}
	return position + 1, true
}
