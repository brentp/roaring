package roaring

type bitmapContainer struct {
	cardinality int
	bitmap      []uint64
}

func newBitmapContainer() *bitmapContainer {
	p := new(bitmapContainer)
	size := (1 << 16) / 64
	p.bitmap = make([]uint64, size, size)
	return p
}

func newBitmapContainerwithRange(firstOfRun, lastOfRun int) *bitmapContainer {
	this := newBitmapContainer()
	this.cardinality = lastOfRun - firstOfRun + 1
	if this.cardinality == maxCapacity {
		fill(this.bitmap, uint64(0xffffffffffffffff))
	} else {
		firstWord := firstOfRun / 64
		lastWord := lastOfRun / 64
		zeroPrefixLength := uint64(firstOfRun & 63)
		zeroSuffixLength := uint64(63 - (lastOfRun & 63))

		fillRange(this.bitmap, firstWord, lastWord+1, uint64(0xffffffffffffffff))
		this.bitmap[firstWord] ^= ((uint64(1) << zeroPrefixLength) - 1)
		blockOfOnes := (uint64(1) << zeroSuffixLength) - 1
		maskOnLeft := blockOfOnes << (uint64(64) - zeroSuffixLength)
		this.bitmap[lastWord] ^= maskOnLeft
	}
	return this
}

type bitmapContainerShortIterator struct {
	ptr *bitmapContainer
	i   int
}

func (bcsi *bitmapContainerShortIterator) next() uint16 {
	j := bcsi.i
	bcsi.i = bcsi.ptr.NextSetBit(bcsi.i + 1)
	return uint16(j)
}
func (bcsi *bitmapContainerShortIterator) hasNext() bool {
	return bcsi.i >= 0
}
func newBitmapContainerShortIterator(a *bitmapContainer) *bitmapContainerShortIterator {
	return &bitmapContainerShortIterator{a, a.NextSetBit(0)}
}
func (bc *bitmapContainer) getShortIterator() shortIterable {
	return newBitmapContainerShortIterator(bc)
}

func (bc *bitmapContainer) getSizeInBytes() int {
	return len(bc.bitmap) * 8
}

func (bc *bitmapContainer) serializedSizeInBytes() int {
	return len(bc.bitmap) * 8
}

func bitmapEquals(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func (bc *bitmapContainer) fillLeastSignificant16bits(x []uint32, i int, mask uint32) {
	// TODO: should be written as optimized assembly
	pos := i
	for k := 0; k < len(bc.bitmap); k++ {
		bitset := bc.bitmap[k]
		for bitset != 0 {
			t := bitset & -bitset
			x[pos] = (uint32(k)*64 + uint32(popcount(t-1))) | mask
			pos++
			bitset ^= t
		}
	}
}

func (bc *bitmapContainer) equals(o interface{}) bool {
	srb, ok := o.(*bitmapContainer)
	if ok {
		if srb.cardinality != bc.cardinality {
			return false
		}
		return bitmapEquals(bc.bitmap, srb.bitmap)
	}
	return false
}

func (bc *bitmapContainer) add(i uint16) container {
	x := int(i)
	previous := bc.bitmap[x/64]
	mask := uint64(1) << (uint(x) % 64)
	newb := previous | mask
	bc.bitmap[x/64] = newb
	bc.cardinality += int(uint64(previous^newb) >> (uint(x) % 64))
	return bc
}

func (bc *bitmapContainer) remove(i uint16) container {
	if bc.contains(i) {
		bc.cardinality--
		bc.bitmap[i/64] &^= (uint64(1) << (i % 64))
		if bc.cardinality == arrayDefaultMaxSize {
			return bc.toArrayContainer()
		}
	}
	return bc
}

func (bc *bitmapContainer) getCardinality() int {
	return bc.cardinality
}

func (bc *bitmapContainer) clone() container {
	ptr := bitmapContainer{bc.cardinality, make([]uint64, len(bc.bitmap))}
	copy(ptr.bitmap, bc.bitmap[:])
	return &ptr
}

func (bc *bitmapContainer) inot(firstOfRange, lastOfRange int) container {
	return bc.NotBitmap(bc, firstOfRange, lastOfRange)
}

func (bc *bitmapContainer) iaddRange(firstOfRange, lastOfRange int) container {
	setBitmapRange(bc.bitmap, firstOfRange, lastOfRange)
	bc.computeCardinality()
	return bc
}

func (bc *bitmapContainer) addRange(firstOfRange, lastOfRange int) container {
	answer := &bitmapContainer{bc.cardinality, make([]uint64, len(bc.bitmap))}
	copy(answer.bitmap, bc.bitmap[:])
	setBitmapRange(answer.bitmap, firstOfRange, lastOfRange)
	answer.computeCardinality()
	return answer

}

func (bc *bitmapContainer) removeRange(firstOfRange, lastOfRange int) container {
	answer := &bitmapContainer{bc.cardinality, make([]uint64, len(bc.bitmap))}
	copy(answer.bitmap, bc.bitmap[:])
	resetBitmapRange(answer.bitmap, firstOfRange, lastOfRange)
	answer.computeCardinality()
	if answer.getCardinality() <= arrayDefaultMaxSize {
		return answer.toArrayContainer()
	}
	return answer
}

func (bc *bitmapContainer) iremoveRange(firstOfRange, lastOfRange int) container {
	resetBitmapRange(bc.bitmap, firstOfRange, lastOfRange)
	bc.computeCardinality()
	if bc.getCardinality() <= arrayDefaultMaxSize {
		return bc.toArrayContainer()
	}
	return bc
}

func (bc *bitmapContainer) not(firstOfRange, lastOfRange int) container {
	return bc.NotBitmap(newBitmapContainer(), firstOfRange, lastOfRange)

}
func (bc *bitmapContainer) NotBitmap(answer *bitmapContainer, firstOfRange, lastOfRange int) container {
	// TODO: should be written as optimized assembly
	if (lastOfRange - firstOfRange + 1) == maxCapacity {
		newCardinality := maxCapacity - bc.cardinality
		for k := 0; k < len(bc.bitmap); k++ {
			answer.bitmap[k] = ^bc.bitmap[k]
		}
		answer.cardinality = newCardinality
		if newCardinality <= arrayDefaultMaxSize {
			return answer.toArrayContainer()
		}
		return answer
	}
	// could be optimized to first determine the answer cardinality,
	// rather than update/create bitmap and then possibly convert

	cardinalityChange := 0
	rangeFirstWord := firstOfRange / 64
	rangeFirstBitPos := firstOfRange & 63
	rangeLastWord := lastOfRange / 64
	rangeLastBitPos := lastOfRange & 63

	// if not in place, we need to duplicate stuff before
	// rangeFirstWord and after rangeLastWord
	if answer != bc {
		copy(answer.bitmap, bc.bitmap[:rangeFirstWord])
		base := rangeLastWord + 1
		sz := len(bc.bitmap) - base
		if sz > 0 {
			copy(answer.bitmap[base:], bc.bitmap[base:base+sz])
		}
	}

	// unfortunately, the simple expression gives the wrong mask for
	// rangeLastBitPos==63
	// no branchless way comes to mind
	maskOnLeft := uint64(0xffffffffffffffff)
	if rangeLastBitPos != 63 {
		maskOnLeft = (uint64(1) << uint((rangeLastBitPos+1)%64)) - 1
	}
	mask := uint64(0xffffffffffffffff) // now zero out stuff in the prefix

	mask ^= (uint64(1) << uint(rangeFirstBitPos%64)) - 1

	if rangeFirstWord == rangeLastWord {
		// range starts and ends in same word (may have
		// unchanged bits on both left and right)
		mask &= maskOnLeft
		cardinalityChange = -int(popcount(bc.bitmap[rangeFirstWord]))
		answer.bitmap[rangeFirstWord] = bc.bitmap[rangeFirstWord] ^ mask
		cardinalityChange += int(popcount(answer.bitmap[rangeFirstWord]))
		answer.cardinality = bc.cardinality + cardinalityChange

		if answer.cardinality <= arrayDefaultMaxSize {
			return answer.toArrayContainer()
		}
		return answer
	}

	// range spans words
	cardinalityChange += -int(popcount(bc.bitmap[rangeFirstWord]))
	answer.bitmap[rangeFirstWord] = bc.bitmap[rangeFirstWord] ^ mask
	cardinalityChange += int(popcount(answer.bitmap[rangeFirstWord]))

	cardinalityChange += -int(popcount(bc.bitmap[rangeLastWord]))
	answer.bitmap[rangeLastWord] = bc.bitmap[rangeLastWord] ^ maskOnLeft
	cardinalityChange += int(popcount(answer.bitmap[rangeLastWord]))

	// negate the words, if any, strictly between first and last
	for i := rangeFirstWord + 1; i < rangeLastWord; i++ {
		bcb := bc.bitmap[i]
		cardinalityChange += (64 - 2*int(popcount(bcb)))
		answer.bitmap[i] = ^bcb
	}
	answer.cardinality = bc.cardinality + cardinalityChange

	if answer.cardinality <= arrayDefaultMaxSize {
		return answer.toArrayContainer()
	}
	return answer
}

func (bc *bitmapContainer) or(a container) container {
	switch a.(type) {
	case *arrayContainer:
		return bc.orArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.orBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) ior(a container) container {
	switch a.(type) {
	case *arrayContainer:
		return bc.iorArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.iorBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) lazyIOR(a container) container {
	switch a.(type) {
	case *arrayContainer:
		return bc.lazyIORArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.lazyIORBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) orArray(value2 *arrayContainer) container {
	answer := bc.clone().(*bitmapContainer)
	c := value2.getCardinality()
	for k := 0; k < c; k++ {
		v := value2.content[k]
		i := uint(v) >> 6
		bef := answer.bitmap[i]
		aft := bef | (uint64(1) << (v % 64))
		answer.bitmap[i] = aft
		answer.cardinality += int((bef - aft) >> 63)
	}
	return answer
}

func (bc *bitmapContainer) orBitmap(value2 *bitmapContainer) container {
	answer := newBitmapContainer()
	for k := 0; k < len(answer.bitmap); k++ {
		answer.bitmap[k] = bc.bitmap[k] | value2.bitmap[k]
	}
	answer.computeCardinality()
	return answer
}

func (bc *bitmapContainer) computeCardinality() {
	bc.cardinality = int(popcntSlice(bc.bitmap))
}

func (bc *bitmapContainer) iorArray(value2 *arrayContainer) container {
	answer := bc
	c := value2.getCardinality()
	for k := 0; k < c; k++ {
		vc := value2.content[k]
		i := uint(vc) >> 6
		bef := answer.bitmap[i]
		aft := bef | (uint64(1) << (vc % 64))
		answer.bitmap[i] = aft
		answer.cardinality += int((bef - aft) >> 63)
	}
	return answer
}

func (bc *bitmapContainer) iorBitmap(value2 *bitmapContainer) container {
	answer := bc
	answer.cardinality = 0
	for k := 0; k < len(answer.bitmap); k++ {
		answer.bitmap[k] = bc.bitmap[k] | value2.bitmap[k]
	}
	answer.computeCardinality()
	return answer
}

func (bc *bitmapContainer) lazyIORArray(value2 *arrayContainer) container {
	answer := bc
	c := value2.getCardinality()
	for k := 0; k < c; k++ {
		vc := value2.content[k]
		i := uint(vc) >> 6
		answer.bitmap[i] = answer.bitmap[i] | (uint64(1) << (vc % 64))
	}
	return answer
}

func (bc *bitmapContainer) lazyIORBitmap(value2 *bitmapContainer) container {
	answer := bc
	for k := 0; k < len(answer.bitmap); k++ {
		answer.bitmap[k] = bc.bitmap[k] | value2.bitmap[k]
	}
	return answer
}

func (bc *bitmapContainer) xor(a container) container {
	switch a.(type) {
	case *arrayContainer:
		return bc.xorArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.xorBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) xorArray(value2 *arrayContainer) container {
	answer := bc.clone().(*bitmapContainer)
	c := value2.getCardinality()
	for k := 0; k < c; k++ {
		vc := value2.content[k]
		index := uint(vc) >> 6
		abi := answer.bitmap[index]
		mask := uint64(1) << (vc % 64)
		answer.cardinality += 1 - 2*int((abi&mask)>>(vc%64))
		answer.bitmap[index] = abi ^ mask
	}
	if answer.cardinality <= arrayDefaultMaxSize {
		return answer.toArrayContainer()
	}
	return answer
}

func (bc *bitmapContainer) rank(x uint16) int {
	// TODO: rewrite in assembly
	leftover := (uint(x) + 1) & 63
	if leftover == 0 {
		return int(popcntSlice(bc.bitmap[:(uint(x)+1)/64]))
	} else {
		return int(popcntSlice(bc.bitmap[:(uint(x)+1)/64]) + popcount(bc.bitmap[(uint(x)+1)/64]<<(64-leftover)))
	}
}

func (bc *bitmapContainer) selectInt(x uint16) int {
	remaining := x
	for k := 0; k < len(bc.bitmap); k++ {
		w := popcount(bc.bitmap[k])
		if uint16(w) > remaining {
			return int(k*64 + selectBitPosition(bc.bitmap[k], int(remaining)))
		}
		remaining -= uint16(w)
	}
	return -1
}

func (bc *bitmapContainer) xorBitmap(value2 *bitmapContainer) container {
	newCardinality := int(popcntXorSlice(bc.bitmap, value2.bitmap))

	if newCardinality > arrayDefaultMaxSize {
		answer := newBitmapContainer()
		for k := 0; k < len(answer.bitmap); k++ {
			answer.bitmap[k] = bc.bitmap[k] ^ value2.bitmap[k]
		}
		answer.cardinality = newCardinality
		return answer
	}
	ac := newArrayContainerSize(newCardinality)
	fillArrayXOR(ac.content, bc.bitmap, value2.bitmap)
	ac.content = ac.content[:newCardinality]
	return ac
}

func (bc *bitmapContainer) and(a container) container {
	switch a.(type) {
	case *arrayContainer:
		return bc.andArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.andBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) intersects(a container) bool {
	switch a.(type) {
	case *arrayContainer:
		return bc.intersectsArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.intersectsBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) iand(a container) container {
	switch a.(type) {
	case *arrayContainer:
		return bc.andArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.iandBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) andArray(value2 *arrayContainer) *arrayContainer {
	answer := newArrayContainerCapacity(len(value2.content))
	c := value2.getCardinality()
	for k := 0; k < c; k++ {
		v := value2.content[k]
		if bc.contains(v) {
			answer.content = append(answer.content, v)
		}
	}
	return answer
}

func (bc *bitmapContainer) andBitmap(value2 *bitmapContainer) container {
	newcardinality := int(popcntAndSlice(bc.bitmap, value2.bitmap))
	if newcardinality > arrayDefaultMaxSize {
		answer := newBitmapContainer()
		for k := 0; k < len(answer.bitmap); k++ {
			answer.bitmap[k] = bc.bitmap[k] & value2.bitmap[k]
		}
		answer.cardinality = newcardinality
		return answer
	}
	ac := newArrayContainerSize(newcardinality)
	fillArrayAND(ac.content, bc.bitmap, value2.bitmap)
	ac.content = ac.content[:newcardinality] //not sure why i need this
	return ac

}

func (bc *bitmapContainer) intersectsArray(value2 *arrayContainer) bool {
	c := value2.getCardinality()
	for k := 0; k < c; k++ {
		v := value2.content[k]
		if bc.contains(v) {
			return true
		}
	}
	return false
}

func (bc *bitmapContainer) intersectsBitmap(value2 *bitmapContainer) bool {
	for k := 0; k < len(bc.bitmap); k++ {
		if (bc.bitmap[k] & value2.bitmap[k]) != 0 {
			return true
		}
	}
	return false

}

func (bc *bitmapContainer) iandBitmap(value2 *bitmapContainer) container {
	newcardinality := int(popcntAndSlice(bc.bitmap, value2.bitmap))
	if newcardinality > arrayDefaultMaxSize {
		for k := 0; k < len(bc.bitmap); k++ {
			bc.bitmap[k] = bc.bitmap[k] & value2.bitmap[k]
		}
		bc.cardinality = newcardinality
		return bc
	}
	ac := newArrayContainerSize(newcardinality)
	fillArrayAND(ac.content, bc.bitmap, value2.bitmap)
	ac.content = ac.content[:newcardinality] //not sure why i need this
	return ac

}

func (bc *bitmapContainer) andNot(a container) container {
	switch a.(type) {
	case *arrayContainer:
		return bc.andNotArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.andNotBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) iandNot(a container) container {
	switch a.(type) {
	case *arrayContainer:
		return bc.andNotArray(a.(*arrayContainer))
	case *bitmapContainer:
		return bc.iandNotBitmap(a.(*bitmapContainer))
	}
	panic("should never happen")
}

func (bc *bitmapContainer) andNotArray(value2 *arrayContainer) container {
	answer := bc.clone().(*bitmapContainer)
	c := value2.getCardinality()
	for k := 0; k < c; k++ {
		vc := value2.content[k]
		i := uint(vc) >> 6
		oldv := answer.bitmap[i]
		newv := oldv &^ (uint64(1) << (vc % 64))
		answer.bitmap[i] = newv
		answer.cardinality -= int(uint64(oldv^newv) >> (vc % 64))
	}
	if answer.cardinality <= arrayDefaultMaxSize {
		return answer.toArrayContainer()
	}
	return answer
}

func (bc *bitmapContainer) andNotBitmap(value2 *bitmapContainer) container {
	newCardinality := int(popcntMaskSlice(bc.bitmap, value2.bitmap))
	if newCardinality > arrayDefaultMaxSize {
		answer := newBitmapContainer()
		for k := 0; k < len(answer.bitmap); k++ {
			answer.bitmap[k] = bc.bitmap[k] &^ value2.bitmap[k]
		}
		answer.cardinality = newCardinality
		return answer
	}
	ac := newArrayContainerSize(newCardinality)
	fillArrayANDNOT(ac.content, bc.bitmap, value2.bitmap)
	return ac
}

func (bc *bitmapContainer) iandNotBitmap(value2 *bitmapContainer) container {
	newCardinality := int(popcntMaskSlice(bc.bitmap, value2.bitmap))
	if newCardinality > arrayDefaultMaxSize {
		for k := 0; k < len(bc.bitmap); k++ {
			bc.bitmap[k] = bc.bitmap[k] &^ value2.bitmap[k]
		}
		bc.cardinality = newCardinality
		return bc
	}
	ac := newArrayContainerSize(newCardinality)
	fillArrayANDNOT(ac.content, bc.bitmap, value2.bitmap)
	return ac
}

func (bc *bitmapContainer) contains(i uint16) bool { //testbit
	x := int(i)
	mask := uint64(1) << uint(x%64)
	return (bc.bitmap[x/64] & mask) != 0
}
func (bc *bitmapContainer) loadData(arrayContainer *arrayContainer) {

	bc.cardinality = arrayContainer.getCardinality()
	c := arrayContainer.getCardinality()
	for k := 0; k < c; k++ {
		x := arrayContainer.content[k]
		i := int(x) / 64
		bc.bitmap[i] |= (uint64(1) << uint(x%64))
	}
}

func (bc *bitmapContainer) toArrayContainer() *arrayContainer {
	ac := newArrayContainerCapacity(bc.cardinality)
	ac.loadData(bc)
	return ac
}
func (bc *bitmapContainer) fillArray(container []uint16) {
	//TODO: rewrite in assembly
	pos := 0
	for k := 0; k < len(bc.bitmap); k++ {
		bitset := bc.bitmap[k]
		for bitset != 0 {
			t := bitset & -bitset
			container[pos] = uint16((k*64 + int(popcount(t-1))))
			pos = pos + 1
			bitset ^= t
		}
	}
}

func (bc *bitmapContainer) NextSetBit(i int) int {
	x := i / 64
	if x >= len(bc.bitmap) {
		return -1
	}
	w := bc.bitmap[x]
	w = w >> uint(i%64)
	if w != 0 {
		return i + numberOfTrailingZeros(w)
	}
	x++
	for ; x < len(bc.bitmap); x++ {
		if bc.bitmap[x] != 0 {
			return (x * 64) + numberOfTrailingZeros(bc.bitmap[x])
		}
	}
	return -1
}
