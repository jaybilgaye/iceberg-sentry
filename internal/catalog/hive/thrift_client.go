package hive

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/apache/thrift/lib/go/thrift"
)

// errNoSuchObject is returned when HMS responds with NoSuchObjectException.
var errNoSuchObject = errors.New("hive: no such object")

// thriftClient is a tiny HMS client. We only implement the two RPCs we use:
//
//	Table get_table(1:string dbname, 2:string tbl_name)
//	list<string> get_all_tables(1:string db_name)
//
// Calls follow the Thrift binary protocol message layout. We sidestep
// generated bindings by reading and writing only the fields we care about
// (table.parameters for get_table; the string-list result for
// get_all_tables); other struct fields are skipped via Skip(type).
type thriftClient struct {
	transport thrift.TTransport
	protocol  thrift.TProtocol
	seqID     int32
}

// Close releases the transport.
func (c *thriftClient) Close() error {
	if c.transport == nil {
		return nil
	}
	if cl, ok := c.transport.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}

func (c *thriftClient) nextSeq() int32 {
	c.seqID++
	return c.seqID
}

// getTableParameters calls HMS get_table and returns the table.parameters map.
func (c *thriftClient) getTableParameters(ctx context.Context, db, tbl string) (map[string]string, error) {
	if err := c.writeGetTable(ctx, db, tbl); err != nil {
		return nil, err
	}
	if err := c.transport.Flush(ctx); err != nil {
		return nil, fmt.Errorf("flush: %w", err)
	}
	return c.readGetTableResult(ctx)
}

// getAllTables calls HMS get_all_tables and returns the list of names.
func (c *thriftClient) getAllTables(ctx context.Context, db string) ([]string, error) {
	if err := c.writeGetAllTables(ctx, db); err != nil {
		return nil, err
	}
	if err := c.transport.Flush(ctx); err != nil {
		return nil, fmt.Errorf("flush: %w", err)
	}
	return c.readGetAllTablesResult(ctx)
}

// --- write ---------------------------------------------------------------

func (c *thriftClient) writeGetTable(ctx context.Context, db, tbl string) error {
	p := c.protocol
	if err := p.WriteMessageBegin(ctx, "get_table", thrift.CALL, c.nextSeq()); err != nil {
		return err
	}
	// args struct: 1:string dbname, 2:string tbl_name
	if err := p.WriteStructBegin(ctx, "get_table_args"); err != nil {
		return err
	}
	if err := writeStringField(ctx, p, "dbname", 1, db); err != nil {
		return err
	}
	if err := writeStringField(ctx, p, "tbl_name", 2, tbl); err != nil {
		return err
	}
	if err := p.WriteFieldStop(ctx); err != nil {
		return err
	}
	if err := p.WriteStructEnd(ctx); err != nil {
		return err
	}
	return p.WriteMessageEnd(ctx)
}

func (c *thriftClient) writeGetAllTables(ctx context.Context, db string) error {
	p := c.protocol
	if err := p.WriteMessageBegin(ctx, "get_all_tables", thrift.CALL, c.nextSeq()); err != nil {
		return err
	}
	if err := p.WriteStructBegin(ctx, "get_all_tables_args"); err != nil {
		return err
	}
	if err := writeStringField(ctx, p, "db_name", 1, db); err != nil {
		return err
	}
	if err := p.WriteFieldStop(ctx); err != nil {
		return err
	}
	if err := p.WriteStructEnd(ctx); err != nil {
		return err
	}
	return p.WriteMessageEnd(ctx)
}

func writeStringField(ctx context.Context, p thrift.TProtocol, name string, id int16, v string) error {
	if err := p.WriteFieldBegin(ctx, name, thrift.STRING, id); err != nil {
		return err
	}
	if err := p.WriteString(ctx, v); err != nil {
		return err
	}
	return p.WriteFieldEnd(ctx)
}

// --- read ---------------------------------------------------------------

func (c *thriftClient) readMessageBegin(ctx context.Context, expectedName string) error {
	name, mtype, _, err := c.protocol.ReadMessageBegin(ctx)
	if err != nil {
		return fmt.Errorf("read message begin: %w", err)
	}
	if mtype == thrift.EXCEPTION {
		appErr := thrift.NewTApplicationException(thrift.UNKNOWN_APPLICATION_EXCEPTION, "")
		if err := appErr.Read(ctx, c.protocol); err != nil {
			return err
		}
		_ = c.protocol.ReadMessageEnd(ctx)
		return fmt.Errorf("hive application error: %s", appErr.Error())
	}
	if name != expectedName {
		return fmt.Errorf("unexpected reply name %q (expected %q)", name, expectedName)
	}
	return nil
}

// readGetTableResult parses get_table_result, returning the parameters map.
// get_table_result struct:
//   0: Table success
//   1: MetaException o1
//   2: NoSuchObjectException o2
//
// Table.parameters is field id 12 (`map<string,string>`).
func (c *thriftClient) readGetTableResult(ctx context.Context) (map[string]string, error) {
	if err := c.readMessageBegin(ctx, "get_table"); err != nil {
		return nil, err
	}
	defer func() { _ = c.protocol.ReadMessageEnd(ctx) }()

	if _, err := c.protocol.ReadStructBegin(ctx); err != nil {
		return nil, err
	}
	var params map[string]string
	for {
		_, ftype, fid, err := c.protocol.ReadFieldBegin(ctx)
		if err != nil {
			return nil, err
		}
		if ftype == thrift.STOP {
			break
		}
		switch {
		case fid == 0 && ftype == thrift.STRUCT:
			// Table struct — extract field 12 (parameters); skip the rest.
			p, err := readTableParameters(ctx, c.protocol)
			if err != nil {
				return nil, err
			}
			params = p
		case fid == 1 && ftype == thrift.STRUCT:
			msg, _ := readExceptionMessage(ctx, c.protocol)
			return nil, fmt.Errorf("hive MetaException: %s", msg)
		case fid == 2 && ftype == thrift.STRUCT:
			_, _ = readExceptionMessage(ctx, c.protocol)
			return nil, errNoSuchObject
		default:
			if err := thrift.SkipDefaultDepth(ctx, c.protocol, ftype); err != nil {
				return nil, err
			}
		}
		if err := c.protocol.ReadFieldEnd(ctx); err != nil {
			return nil, err
		}
	}
	if err := c.protocol.ReadStructEnd(ctx); err != nil {
		return nil, err
	}
	if params == nil {
		params = map[string]string{}
	}
	return params, nil
}

// readTableParameters walks a Table struct, returning only field 12 (parameters).
func readTableParameters(ctx context.Context, p thrift.TProtocol) (map[string]string, error) {
	if _, err := p.ReadStructBegin(ctx); err != nil {
		return nil, err
	}
	var out map[string]string
	for {
		_, ftype, fid, err := p.ReadFieldBegin(ctx)
		if err != nil {
			return nil, err
		}
		if ftype == thrift.STOP {
			break
		}
		if fid == 12 && ftype == thrift.MAP {
			m, err := readStringMap(ctx, p)
			if err != nil {
				return nil, err
			}
			out = m
		} else {
			if err := thrift.SkipDefaultDepth(ctx, p, ftype); err != nil {
				return nil, err
			}
		}
		if err := p.ReadFieldEnd(ctx); err != nil {
			return nil, err
		}
	}
	return out, p.ReadStructEnd(ctx)
}

func readStringMap(ctx context.Context, p thrift.TProtocol) (map[string]string, error) {
	kt, vt, size, err := p.ReadMapBegin(ctx)
	if err != nil {
		return nil, err
	}
	if kt != thrift.STRING || vt != thrift.STRING {
		// Not a string→string map; skip to keep the protocol stream aligned.
		for i := 0; i < size; i++ {
			if err := thrift.SkipDefaultDepth(ctx, p, kt); err != nil {
				return nil, err
			}
			if err := thrift.SkipDefaultDepth(ctx, p, vt); err != nil {
				return nil, err
			}
		}
		return nil, p.ReadMapEnd(ctx)
	}
	out := make(map[string]string, size)
	for i := 0; i < size; i++ {
		k, err := p.ReadString(ctx)
		if err != nil {
			return nil, err
		}
		v, err := p.ReadString(ctx)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, p.ReadMapEnd(ctx)
}

// readGetAllTablesResult parses a list<string> success result.
//
// get_all_tables_result struct:
//   0: list<string> success
//   1: MetaException o1
func (c *thriftClient) readGetAllTablesResult(ctx context.Context) ([]string, error) {
	if err := c.readMessageBegin(ctx, "get_all_tables"); err != nil {
		return nil, err
	}
	defer func() { _ = c.protocol.ReadMessageEnd(ctx) }()

	if _, err := c.protocol.ReadStructBegin(ctx); err != nil {
		return nil, err
	}
	var out []string
	for {
		_, ftype, fid, err := c.protocol.ReadFieldBegin(ctx)
		if err != nil {
			return nil, err
		}
		if ftype == thrift.STOP {
			break
		}
		switch {
		case fid == 0 && ftype == thrift.LIST:
			et, size, err := c.protocol.ReadListBegin(ctx)
			if err != nil {
				return nil, err
			}
			if et != thrift.STRING {
				for i := 0; i < size; i++ {
					if err := thrift.SkipDefaultDepth(ctx, c.protocol, et); err != nil {
						return nil, err
					}
				}
			} else {
				out = make([]string, 0, size)
				for i := 0; i < size; i++ {
					s, err := c.protocol.ReadString(ctx)
					if err != nil {
						return nil, err
					}
					out = append(out, s)
				}
			}
			if err := c.protocol.ReadListEnd(ctx); err != nil {
				return nil, err
			}
		case fid == 1 && ftype == thrift.STRUCT:
			msg, _ := readExceptionMessage(ctx, c.protocol)
			return nil, fmt.Errorf("hive MetaException: %s", msg)
		default:
			if err := thrift.SkipDefaultDepth(ctx, c.protocol, ftype); err != nil {
				return nil, err
			}
		}
		if err := c.protocol.ReadFieldEnd(ctx); err != nil {
			return nil, err
		}
	}
	return out, c.protocol.ReadStructEnd(ctx)
}

// readExceptionMessage extracts the `message` field (id 1) from an HMS exception struct.
func readExceptionMessage(ctx context.Context, p thrift.TProtocol) (string, error) {
	if _, err := p.ReadStructBegin(ctx); err != nil {
		return "", err
	}
	var msg string
	for {
		_, ftype, fid, err := p.ReadFieldBegin(ctx)
		if err != nil {
			return "", err
		}
		if ftype == thrift.STOP {
			break
		}
		if fid == 1 && ftype == thrift.STRING {
			s, err := p.ReadString(ctx)
			if err != nil {
				return "", err
			}
			msg = s
		} else {
			if err := thrift.SkipDefaultDepth(ctx, p, ftype); err != nil {
				return "", err
			}
		}
		if err := p.ReadFieldEnd(ctx); err != nil {
			return "", err
		}
	}
	return msg, p.ReadStructEnd(ctx)
}
