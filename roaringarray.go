package roaring

import (
	"encoding/binary"
	"io"
)

type container interface {
	clone() container
	and(container) container
	iand(container) container // i stands for inplace
	andNot(container) container
	iandNot(container) container // i stands for inplace
	getCardinality() int
	rank(uint16) int
	add(uint16) container
	addRange(start, final int) container  // range is [firstOfRange,lastOfRange)
	iaddRange(start, final int) container // i stands for inplace, range is [firstOfRange,lastOfRange)
	remove(uint16) container
	not(start, final int) container               // range is [firstOfRange,lastOfRange]
	inot(firstOfRange, lastOfRange int) container // i stands for inplace, range is [firstOfRange,lastOfRange]
	xor(r container) container
	getShortIterator() shortIterable
	contains(i uint16) bool
	equals(i interface{}) bool
	fillLeastSignificant16bits(array []uint32, i int, mask uint32)
	or(r container) container
	ior(r container) container   // i stands for inplace
	intersects(r container) bool // whether the two containers intersect
	lazyIOR(r container) container
	getSizeInBytes() int
	removeRange(start, final int) container  // range is [firstOfRange,lastOfRange)
	iremoveRange(start, final int) container // i stands for inplace, range is [firstOfRange,lastOfRange)
	selectInt(uint16) int
	serializedSizeInBytes() int
	readFrom(io.Reader) (int, error)
	writeTo(io.Writer) (int, error)
}

// careful: range is [firstOfRange,lastOfRange]
func rangeOfOnes(start, last int) container {
	if (last - start + 1) > arrayDefaultMaxSize {
		return newBitmapContainerwithRange(start, last)
	}

	return newArrayContainerRange(start, last)
}

type roaringArray struct {
	keys       []uint16
	containers []container
	dirty      []bool
}

func newRoaringArray() *roaringArray {
	ra := &roaringArray{}
	ra.clear()

	return ra
}

func (ra *roaringArray) appendContainer(key uint16, value container) {
	ra.keys = append(ra.keys, key)
	ra.containers = append(ra.containers, value)
	if ra.hasDirty() {
		ra.dirty = append(ra.dirty, false)
	}
}

func (ra *roaringArray) appendCopy(sa roaringArray, startingindex int) {
	ra.appendContainer(sa.keys[startingindex], sa.containers[startingindex].clone())
}

func (ra *roaringArray) appendCopyMany(sa roaringArray, startingindex, end int) {
	for i := startingindex; i < end; i++ {
		ra.appendCopy(sa, i)
	}
}

func (ra *roaringArray) appendCopiesUntil(sa roaringArray, stoppingKey uint16) {
	for i := 0; i < sa.size(); i++ {
		if sa.keys[i] >= stoppingKey {
			break
		}
		ra.appendContainer(sa.keys[i], sa.containers[i].clone())
	}
}

func (ra *roaringArray) appendCopiesAfter(sa roaringArray, beforeStart uint16) {
	startLocation := sa.getIndex(beforeStart)
	if startLocation >= 0 {
		startLocation++
	} else {
		startLocation = -startLocation - 1
	}

	for i := startLocation; i < sa.size(); i++ {
		ra.appendContainer(sa.keys[i], sa.containers[i].clone())
	}
}

func (ra *roaringArray) removeIndexRange(begin, end int) {
	if end <= begin {
		return
	}

	r := end - begin

	copy(ra.keys[begin:], ra.keys[end:])
	copy(ra.containers[begin:], ra.containers[end:])
	if ra.hasDirty() {
		copy(ra.dirty[begin:], ra.dirty[end:])
	}

	ra.resize(len(ra.keys) - r)
}

func (ra *roaringArray) resize(newsize int) {
	for k := newsize; k < len(ra.containers); k++ {
		ra.containers[k] = nil
	}

	ra.keys = ra.keys[:newsize]
	ra.containers = ra.containers[:newsize]
	if ra.hasDirty() {
		ra.dirty = ra.dirty[:newsize]
	}
}

func (ra *roaringArray) clear() {
	ra.keys = make([]uint16, 0)
	ra.containers = make([]container, 0)
	ra.dirty = make([]bool, 0)
}

func (ra *roaringArray) clone() *roaringArray {
	sa := new(roaringArray)
	sa.keys = make([]uint16, len(ra.keys))
	sa.containers = make([]container, len(ra.containers))
	sa.dirty = make([]bool, len(ra.keys))

	copy(sa.keys, ra.keys)
	copy(sa.containers, ra.containers)

	sa.markAllDirty()
	ra.markAllDirty()

	return sa
}

func (ra *roaringArray) containsKey(x uint16) bool {
	return (ra.binarySearch(0, len(ra.keys), x) >= 0)
}

func (ra *roaringArray) getContainer(x uint16) container {
	i := ra.binarySearch(0, len(ra.keys), x)
	if i < 0 {
		return nil
	}
	return ra.containers[i]
}

func (ra *roaringArray) getContainerAtIndex(i int) container {
	return ra.containers[i]
}

func (ra *roaringArray) getWritableContainerAtIndex(i int) container {
	if len(ra.dirty) > 0 && ra.dirty[i] {
		ra.containers[i] = ra.containers[i].clone()
		ra.dirty[i] = false
	}
	return ra.containers[i]
}

func (ra *roaringArray) getIndex(x uint16) int {
	// before the binary search, we optimize for frequent cases
	size := len(ra.keys)
	if (size == 0) || (ra.keys[size-1] == x) {
		return size - 1
	}
	return ra.binarySearch(0, size, x)
}

func (ra *roaringArray) getKeyAtIndex(i int) uint16 {
	return ra.keys[i]
}

func (ra *roaringArray) insertNewKeyValueAt(i int, key uint16, value container) {
	ra.keys = append(ra.keys, 0)
	ra.containers = append(ra.containers, nil)

	copy(ra.keys[i+1:], ra.keys[i:])
	copy(ra.containers[i+1:], ra.containers[i:])

	ra.keys[i] = key
	ra.containers[i] = value

	if ra.hasDirty() {
		ra.dirty = append(ra.dirty, false)
		copy(ra.dirty[i+1:], ra.dirty[i:])
		ra.dirty[i] = false
	}
}

func (ra *roaringArray) remove(key uint16) bool {
	i := ra.binarySearch(0, len(ra.keys), key)
	if i >= 0 { // if a new key
		ra.removeAtIndex(i)
		return true
	}
	return false
}

func (ra *roaringArray) removeAtIndex(i int) {
	copy(ra.keys[i:], ra.keys[i+1:])
	copy(ra.containers[i:], ra.containers[i+1:])

	if ra.hasDirty() {
		copy(ra.dirty[i:], ra.dirty[i+1:])
	}

	ra.resize(len(ra.keys) - 1)
}

func (ra *roaringArray) setContainerAtIndex(i int, c container) {
	ra.containers[i] = c
}

func (ra *roaringArray) replaceKeyAndContainerAtIndex(i int, key uint16, c container) {
	ra.keys[i] = key
	ra.containers[i] = c

	if ra.hasDirty() {
		ra.dirty[i] = false
	}
}

func (ra *roaringArray) size() int {
	return len(ra.keys)
}

func (ra *roaringArray) binarySearch(begin, end int, ikey uint16) int {
	low := begin
	high := end - 1
	for low+16 <= high {
		middleIndex := int(uint((low + high)) >> 1)
		middleValue := ra.keys[middleIndex]

		if middleValue < ikey {
			low = middleIndex + 1
		} else if middleValue > ikey {
			high = middleIndex - 1
		} else {
			return middleIndex
		}
	}
	for ; low <= high; low++ {
		val := ra.keys[low]
		if val >= ikey {
			if val == ikey {
				return low
			}
			break
		}
	}
	return -(low + 1)
}

func (ra *roaringArray) equals(o interface{}) bool {
	srb, ok := o.(roaringArray)
	if ok {

		if srb.size() != ra.size() {
			return false
		}
		for i, k := range ra.keys {
			if k != srb.keys[i] {
				return false
			}
		}

		for i, c := range ra.containers {
			if !c.equals(srb.containers[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (ra *roaringArray) serializedSizeInBytes() uint64 {
	count := uint64(4 + 4)
	for _, c := range ra.containers {
		count = count + 4 + 4
		count = count + uint64(c.serializedSizeInBytes())
	}
	return count
}

func (ra *roaringArray) writeTo(stream io.Writer) (int, error) {
	preambleSize := 4 + 4 + 4*len(ra.keys)
	buf := make([]byte, preambleSize+4*len(ra.keys))
	binary.LittleEndian.PutUint32(buf[0:], uint32(serial_cookie))
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(ra.keys)))

	for i, key := range ra.keys {
		off := 8 + i*4
		binary.LittleEndian.PutUint16(buf[off:], uint16(key))

		c := ra.containers[i]
		binary.LittleEndian.PutUint16(buf[off+2:], uint16(c.getCardinality()-1))
	}

	startOffset := preambleSize + 4*len(ra.keys)
	for i, c := range ra.containers {
		binary.LittleEndian.PutUint32(buf[preambleSize+i*4:], uint32(startOffset))
		startOffset += getSizeInBytesFromCardinality(c.getCardinality())
	}

	_, err := stream.Write(buf)
	if err != nil {
		return 0, err
	}

	for _, c := range ra.containers {
		_, err := c.writeTo(stream)
		if err != nil {
			return 0, err
		}
	}
	return startOffset, nil
}

func (ra *roaringArray) readFrom(stream io.Reader) (int, error) {
	var cookie uint32
	err := binary.Read(stream, binary.LittleEndian, &cookie)
	if err != nil {
		return 0, err
	}
	if cookie != serial_cookie {
		return 0, err
	}
	var size uint32
	err = binary.Read(stream, binary.LittleEndian, &size)
	if err != nil {
		return 0, err
	}
	keycard := make([]uint16, 2*size, 2*size)
	err = binary.Read(stream, binary.LittleEndian, keycard)
	if err != nil {
		return 0, err
	}
	offsets := make([]uint32, size, size)
	err = binary.Read(stream, binary.LittleEndian, offsets)
	if err != nil {
		return 0, err
	}
	offset := int(4 + 4 + 8*size)
	for i := uint32(0); i < size; i++ {
		c := int(keycard[2*i+1]) + 1
		offset += int(getSizeInBytesFromCardinality(c))
		if c > arrayDefaultMaxSize {
			nb := newBitmapContainer()
			nb.readFrom(stream)
			nb.cardinality = int(c)
			ra.appendContainer(keycard[2*i], nb)
		} else {
			nb := newArrayContainerSize(int(c))
			nb.readFrom(stream)
			ra.appendContainer(keycard[2*i], nb)
		}
	}
	return offset, nil
}

func (ra *roaringArray) advanceUntil(min uint16, pos int) int {
	lower := pos + 1

	if lower >= len(ra.keys) || ra.keys[lower] >= min {
		return lower
	}

	spansize := 1

	for lower+spansize < len(ra.keys) && ra.keys[lower+spansize] < min {
		spansize *= 2
	}
	var upper int
	if lower+spansize < len(ra.keys) {
		upper = lower + spansize
	} else {
		upper = len(ra.keys) - 1
	}

	if ra.keys[upper] == min {
		return upper
	}

	if ra.keys[upper] < min {
		// means
		// array
		// has no
		// item
		// >= min
		// pos = array.length;
		return len(ra.keys)
	}

	// we know that the next-smallest span was too small
	lower += (spansize / 2)

	mid := 0
	for lower+1 != upper {
		mid = (lower + upper) / 2
		if ra.keys[mid] == min {
			return mid
		} else if ra.keys[mid] < min {
			lower = mid
		} else {
			upper = mid
		}
	}
	return upper
}

func (ra *roaringArray) markAllDirty() {
	dirty := make([]bool, len(ra.keys))
	for i := range dirty {
		dirty[i] = true
	}
	ra.dirty = dirty
}

func (ra *roaringArray) hasDirty() bool {
	return len(ra.dirty) > 0
}

func (ra *roaringArray) isDirty(i int) bool {
	return ra.hasDirty() && ra.dirty[i]
}
