package exif

import (
    "bytes"
    "fmt"
    "strings"

    "encoding/binary"

    "github.com/dsoprea/go-logging"
)

var (
    ifdLogger = log.NewLogger("exifjpeg.ifd")
)


// IfdTagEnumerator knows how to decode an IFD and all of the tags it
// describes. Note that the IFDs and the actual values floating throughout the
// whole EXIF block, but the IFD itself has just a minor header and a set of
// repeating, statically-sized records. So, the tags (though not their values)
// are fairly simple to enumerate.
type IfdTagEnumerator struct {
    byteOrder binary.ByteOrder
    rawExif []byte
    ifdOffset uint32
    buffer *bytes.Buffer
}

func NewIfdTagEnumerator(rawExif []byte, byteOrder binary.ByteOrder, ifdOffset uint32) (ite *IfdTagEnumerator) {
    ite = &IfdTagEnumerator{
        rawExif: rawExif,
        byteOrder: byteOrder,
        buffer: bytes.NewBuffer(rawExif[ifdOffset:]),
    }

    return ite
}

// getUint16 reads a uint16 and advances both our current and our current
// accumulator (which allows us to know how far to seek to the beginning of the
// next IFD when it's time to jump).
func (ife *IfdTagEnumerator) getUint16() (value uint16, raw []byte, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    raw = make([]byte, 2)

    _, err = ife.buffer.Read(raw)
    log.PanicIf(err)

    if ife.byteOrder == binary.BigEndian {
        value = binary.BigEndian.Uint16(raw)
    } else {
        value = binary.LittleEndian.Uint16(raw)
    }

    return value, raw, nil
}

// getUint32 reads a uint32 and advances both our current and our current
// accumulator (which allows us to know how far to seek to the beginning of the
// next IFD when it's time to jump).
func (ife *IfdTagEnumerator) getUint32() (value uint32, raw []byte, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    raw = make([]byte, 4)

    _, err = ife.buffer.Read(raw)
    log.PanicIf(err)

    if ife.byteOrder == binary.BigEndian {
        value = binary.BigEndian.Uint32(raw)
    } else {
        value = binary.LittleEndian.Uint32(raw)
    }

    return value, raw, nil
}


type IfdEnumerate struct {
    data []byte
    buffer *bytes.Buffer
    byteOrder binary.ByteOrder
    currentOffset uint32
    ifdTopOffset uint32
}

func NewIfdEnumerate(data []byte, byteOrder binary.ByteOrder) *IfdEnumerate {
    return &IfdEnumerate{
        data: data,
        buffer: bytes.NewBuffer(data),
        byteOrder: byteOrder,
        ifdTopOffset: 6,
    }
}

// ValueContext describes all of the parameters required to find and extract
// the actual tag value.
type ValueContext struct {
    UnitCount uint32
    ValueOffset uint32
    RawValueOffset []byte
    RawExif []byte
}

func (ie *IfdEnumerate) getTagEnumerator(ifdOffset uint32) (ite *IfdTagEnumerator) {
    ite = NewIfdTagEnumerator(
            ie.data[ie.ifdTopOffset:],
            ie.byteOrder,
            ifdOffset)

    return ite
}

// TagVisitor is an optional callback that can get hit for every tag we parse
// through. `rawExif` is the byte array startign after the EXIF header (where
// the offsets of all IFDs and values are calculated from).
type TagVisitor func(indexedIfdName string, tagId uint16, tagType TagType, valueContext ValueContext) (err error)


type IfdTagEntry struct {
    TagId uint16
    TagIndex int
    TagType uint16
    UnitCount uint32
    ValueOffset uint32
    RawValueOffset []byte
    IfdName string
}


// ParseIfd decodes the IFD block that we're currently sitting on the first
// byte of.
func (ie *IfdEnumerate) ParseIfd(ifdName string, ifdIndex int, ifdOffset uint32, visitor TagVisitor, doDescend bool) (nextIfdOffset uint32, entries []IfdTagEntry, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    ifdLogger.Debugf(nil, "Parsing IFD [%s] (%d) at offset (%04x).", ifdName, ifdIndex, ifdOffset)

    // Return the name of the IFD as its known in our tag-index. We should skip
    // over the current IFD if this is empty (which means we don't recognize/
    // understand the IFD and, therefore, don't know the tags that are valid for
    // it). Note that we could leave ignoring the tags as a responsibility for
    // the visitor, but then it'd be easy for people to integrate that logic and
    // not realize that they needed to specially handle an empty IFD name until
    // they happened upon some obscure media one day and suddenly have issue if
    // they unwittingly write something that breaks in that situation.
    indexedIfdName := IfdName(ifdName, ifdIndex)
    if indexedIfdName == "" {
        ifdLogger.Debugf(nil, "IFD not known and will not be visited: [%s] (%d)", ifdName, ifdIndex)
    }

    ite := ie.getTagEnumerator(ifdOffset)

    tagCount, _, err := ite.getUint16()
    log.PanicIf(err)

    ifdLogger.Debugf(nil, "Current IFD tag-count: (%d)", tagCount)

    entries = make([]IfdTagEntry, tagCount)

    for i := uint16(0); i < tagCount; i++ {
        tagId, _, err := ite.getUint16()
        log.PanicIf(err)

        tagType, _, err := ite.getUint16()
        log.PanicIf(err)

        unitCount, _, err := ite.getUint32()
        log.PanicIf(err)

        valueOffset, rawValueOffset, err := ite.getUint32()
        log.PanicIf(err)

        if visitor != nil && indexedIfdName != "" {
            tt := NewTagType(tagType, ie.byteOrder)

            vc := ValueContext{
                UnitCount: unitCount,
                ValueOffset: valueOffset,
                RawValueOffset: rawValueOffset,
                RawExif: ie.data[ie.ifdTopOffset:],
            }

            err := visitor(indexedIfdName, tagId, tt, vc)
            log.PanicIf(err)
        }

        tag := IfdTagEntry{
            TagId: tagId,
            TagIndex: int(i),
            TagType: tagType,
            UnitCount: unitCount,
            ValueOffset: valueOffset,
            RawValueOffset: rawValueOffset,
        }

        childIfdName, isIfd := IsIfdTag(tagId)
        if isIfd == true {
            tag.IfdName = childIfdName

            if doDescend == true {
                ifdLogger.Debugf(nil, "Descending to IFD [%s].", childIfdName)

                err := ie.Scan(childIfdName, valueOffset, visitor)
                log.PanicIf(err)
            }
        }

        entries[i] = tag
    }

    nextIfdOffset, _, err = ite.getUint32()
    log.PanicIf(err)

    ifdLogger.Debugf(nil, "Next IFD at offset: (%08x)", nextIfdOffset)

    return nextIfdOffset, entries, nil
}

// Scan enumerates the different EXIF blocks (called IFDs).
func (ie *IfdEnumerate) Scan(ifdName string, ifdOffset uint32, visitor TagVisitor) (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    for ifdIndex := 0;; ifdIndex++ {
        nextIfdOffset, _, err := ie.ParseIfd(ifdName, ifdIndex, ifdOffset, visitor, true)
        log.PanicIf(err)

        if nextIfdOffset == 0 {
            break
        }

        ifdOffset = nextIfdOffset
    }

    return nil
}


type Ifd struct {
    Id int
    ParentIfd *Ifd
    Name string
    Index int
    Offset uint32
    Entries []IfdTagEntry
    Children []*Ifd
    NextIfdOffset uint32
    NextIfd *Ifd
}

func (ifd Ifd) String() string {
    parentOffset := uint32(0)
    if ifd.ParentIfd != nil {
        parentOffset = ifd.ParentIfd.Offset
    }

    return fmt.Sprintf("IFD<ID=(%d) N=[%s] IDX=(%d) OFF=(0x%04x) COUNT=(%d) CHILDREN=(%d) PARENT=(0x%04x) NEXT-IFD=(0x%04x)", ifd.Id, ifd.Name, ifd.Index, ifd.Offset, len(ifd.Entries), len(ifd.Children), parentOffset, ifd.NextIfdOffset)
}

func (ifd Ifd) printNode(level int, nextLink bool) {
    indent := strings.Repeat(" ", level * 2)

    prefix := " "
    if nextLink {
        prefix = ">"
    }

    fmt.Printf("%s%s%s\n", indent, prefix, ifd)

    for _, childIfd := range ifd.Children {
        childIfd.printNode(level + 1, false)
    }

    if ifd.NextIfd != nil {
        ifd.NextIfd.printNode(level, true)
    }
}

func (ifd Ifd) PrintTree() {
    ifd.printNode(0, false)
}


type QueuedIfd struct {
    Name string
    Index int
    Offset uint32
    Parent *Ifd
}

// Scan enumerates the different EXIF blocks (called IFDs).
func (ie *IfdEnumerate) Collect(rootIfdOffset uint32) (rootIfd *Ifd, tree map[int]*Ifd, ifds []*Ifd, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    tree = make(map[int]*Ifd)
    ifds = make([]*Ifd, 0)

    queue := []QueuedIfd {
        {
            Name: IfdStandard,
            Index: 0,
            Offset: rootIfdOffset,
        },
    }

    edges := make(map[uint32]*Ifd)

    for {
        if len(queue) == 0 {
            break
        }

        name := queue[0].Name
        index := queue[0].Index
        offset := queue[0].Offset
        parentIfd := queue[0].Parent

        queue = queue[1:]

        nextIfdOffset, entries, err := ie.ParseIfd(name, index, offset, nil, false)
        log.PanicIf(err)

        id := len(ifds)

        ifd := Ifd{
            Id: id,
            ParentIfd: parentIfd,
            Name: name,
            Index: index,
            Offset: offset,
            Entries: entries,
            Children: make([]*Ifd, 0),
            NextIfdOffset: nextIfdOffset,
        }

        // Add ourselves to a big list of IFDs.
        ifds = append(ifds, &ifd)

        // Install ourselves into a lookup table.
        tree[id] = &ifd

        // Add a link from the previous IFD in the chain to us.
        if previousIfd, found := edges[offset]; found == true {
            previousIfd.NextIfd = &ifd
        }

        // Attach as a child to our parent (where we appeared as a tag in
        // that IFD).
        if parentIfd != nil {
            parentIfd.Children = append(parentIfd.Children, &ifd)
        }

        // Determine if any of our entries is a child IFD and queue it.
        for _, entry := range entries {
            if entry.IfdName == "" {
                continue
            }

            qi := QueuedIfd {
                Name: entry.IfdName,
                Index: 0,
                Offset: entry.ValueOffset,
                Parent: &ifd,
            }

            queue = append(queue, qi)
        }

        // If there's another IFD in the chain.
        if nextIfdOffset != 0 {
            // Allow the next link to know what the previous link was.
            edges[nextIfdOffset] = &ifd

            qi := QueuedIfd {
                Name: IfdStandard,
                Index: index + 1,
                Offset: nextIfdOffset,
            }

            queue = append(queue, qi)
        }
    }

    return tree[0], tree, ifds, nil
}
