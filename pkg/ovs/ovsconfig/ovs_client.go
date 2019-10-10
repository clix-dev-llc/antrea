package ovsconfig

import (
	"errors"
	"time"

	"github.com/TomCodeLV/OVSDB-golang-lib/pkg/dbtransaction"
	"github.com/TomCodeLV/OVSDB-golang-lib/pkg/helpers"
	"github.com/TomCodeLV/OVSDB-golang-lib/pkg/ovsdb"
	"github.com/TomCodeLV/OVSDB-golang-lib/pkg/ovshelper"
	"k8s.io/klog"
)

type OVSBridge struct {
	ovsdb *ovsdb.OVSDB
	name  string
	uuid  string
}

type OVSPortData struct {
	UUID        string
	Name        string
	ExternalIDs map[string]string
	IFName      string
	OFPort      int32
}

const defaultUDSAddress = "/run/openvswitch/db.sock"

const openvSwitchSchema = "Open_vSwitch"

// Connects to the OVSDB server on the UNIX domain socket specified by address.
// If address is set to "", the default UNIX domain socket path
// "/run/openvswitch/db.sock" will be used.
// Returns the OVSDB struct on success.
func NewOVSDBConnectionUDS(address string) (*ovsdb.OVSDB, Error) {
	klog.Infof("Connecting to OVSDB at address %s", address)

	if address == "" {
		address = defaultUDSAddress
	}

	// For the sake of debugging, we keep logging messages until the
	// connection is succesful. We use exponential backoff to determine the
	// sleep  duration between two successive log messages (up to
	// maxBackoffTime).
	const maxBackoffTime = 8 * time.Second
	success := make(chan bool, 1)
	go func() {
		backoff := 1 * time.Second
		for {
			select {
			case <-success:
				return
			case <-time.After(backoff):
				backoff *= 2
				if backoff > maxBackoffTime {
					backoff = maxBackoffTime
				}
				klog.Infof("Not connected yet, will try again in %v", backoff)
			}
		}
	}()

	db := ovsdb.Dial([][]string{{"unix", address}}, nil, nil)
	success <- true
	return db, nil
}

// Creates and returns a new OVSBridge struct.
func NewOVSBridge(bridgeName string, ovsdb *ovsdb.OVSDB) *OVSBridge {
	return &OVSBridge{ovsdb, bridgeName, ""}
}

// Looks up or creates the bridge.
// If the bridge with name bridgeName does not exist, it will be created.
func (br *OVSBridge) Create() Error {
	if exists, err := br.lookupByName(); err != nil {
		return err
	} else if exists {
		klog.Info("Bridge exists: ", br.uuid)
	} else if err = br.create(); err != nil {
		return err
	} else {
		klog.Info("Created bridge: ", br.uuid)
	}

	return nil
}

func (br *OVSBridge) lookupByName() (bool, Error) {
	tx := br.ovsdb.Transaction(openvSwitchSchema)
	tx.Select(dbtransaction.Select{
		Table:   "Bridge",
		Columns: []string{"_uuid"},
		Where:   [][]interface{}{{"name", "==", br.name}},
	})
	res, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return false, newTransactionError(err, temporary)
	}

	if len(res[0].Rows) == 0 {
		return false, nil
	}

	br.uuid = res[0].Rows[0].(map[string]interface{})["_uuid"].([]interface{})[1].(string)
	return true, nil
}

func (br *OVSBridge) create() Error {
	tx := br.ovsdb.Transaction(openvSwitchSchema)
	bridge := ovshelper.Bridge{
		Name: br.name,
	}
	namedUUID := tx.Insert(dbtransaction.Insert{
		Table: "Bridge",
		Row:   bridge,
	})

	mutateSet := helpers.MakeOVSDBSet(map[string]interface{}{
		"named-uuid": []string{namedUUID},
	})
	tx.Mutate(dbtransaction.Mutate{
		Table:     "Open_vSwitch",
		Mutations: [][]interface{}{{"bridges", "insert", mutateSet}},
	})

	res, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return newTransactionError(err, temporary)
	}

	br.uuid = res[0].UUID[1]
	return nil
}

func (br *OVSBridge) Delete() Error {
	tx := br.ovsdb.Transaction(openvSwitchSchema)
	mutateSet := helpers.MakeOVSDBSet(map[string]interface{}{
		"uuid": []string{br.uuid},
	})
	tx.Mutate(dbtransaction.Mutate{
		Table:     "Open_vSwitch",
		Mutations: [][]interface{}{{"bridges", "delete", mutateSet}},
	})

	_, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return newTransactionError(err, temporary)
	}
	return nil
}

// Return UUIDs of all ports on the bridge
func (br *OVSBridge) getPortUUIDList() ([]string, Error) {
	tx := br.ovsdb.Transaction(openvSwitchSchema)
	tx.Select(dbtransaction.Select{
		Table:   "Bridge",
		Columns: []string{"ports"},
		Where:   [][]interface{}{{"name", "==", br.name}},
	})

	res, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return nil, newTransactionError(err, temporary)
	}

	portRes := res[0].Rows[0].(map[string]interface{})["ports"].([]interface{})
	return helpers.GetIdListFromOVSDBSet(portRes), nil
}

// Delete ports in portUUIDList on the bridge
func (br *OVSBridge) DeletePorts(portUUIDList []string) Error {
	tx := br.ovsdb.Transaction(openvSwitchSchema)
	mutateSet := helpers.MakeOVSDBSet(map[string]interface{}{
		"uuid": portUUIDList,
	})
	tx.Mutate(dbtransaction.Mutate{
		Table:     "Bridge",
		Mutations: [][]interface{}{{"ports", "delete", mutateSet}},
	})

	_, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return newTransactionError(err, temporary)
	}
	return nil
}

func (br *OVSBridge) DeletePort(portUUID string) Error {
	tx := br.ovsdb.Transaction(openvSwitchSchema)
	mutateSet := helpers.MakeOVSDBSet(map[string]interface{}{
		"uuid": []string{portUUID},
	})
	tx.Mutate(dbtransaction.Mutate{
		Table:     "Bridge",
		Mutations: [][]interface{}{{"ports", "delete", mutateSet}},
	})

	_, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return newTransactionError(err, temporary)
	}
	return nil
}

// Creates an internal port with the specified name on the bridge.
// If externalIDs is not empty, the map key/value pairs will be set to the
// port's external_ids.
// If ofPortRequest is not zero, it will be passed to the OVS port creation.
func (br *OVSBridge) CreateInternalPort(name string, ofPortRequest int32, externalIDs map[string]interface{}) (string, Error) {
	return br.createPort(name, name, "internal", ofPortRequest, externalIDs, nil)
}

// Creates a VXLAN tunnel port with the specified name on the bridge.
// If ofPortRequest is not zero, it will be passed to the OVS port creation.
// If remoteIP is not empty, it will be set to the tunnel port interface
// options; otherwise flow based tunneling will be configured.
func (br *OVSBridge) CreateVXLANPort(name string, ofPortRequest int32, remoteIP string) (string, Error) {
	return br.createTunnelPort(name, "vxlan", ofPortRequest, remoteIP)
}

// Creates a Geneve tunnel port with the specified name on the bridge.
// If ofPortRequest is not zero, it will be passed to the OVS port creation.
// If remoteIP is not empty, it will be set to the tunnel port interface
// options; otherwise flow based tunneling will be configured.
func (br *OVSBridge) CreateGenevePort(name string, ofPortRequest int32, remoteIP string) (string, Error) {
	return br.createTunnelPort(name, "geneve", ofPortRequest, remoteIP)
}

func (br *OVSBridge) createTunnelPort(name, ifType string, ofPortRequest int32, remoteIP string) (string, Error) {
	var options map[string]interface{}
	if remoteIP != "" {
		options = map[string]interface{}{"remote_ip": remoteIP}
	} else {
		options = map[string]interface{}{"key": "flow", "remote_ip": "flow"}
	}
	return br.createPort(name, name, ifType, ofPortRequest, nil, options)
}

// Creates a port with the specified name on the bridge, and connects interface
// specified by ifDev to the port.
// If externalIDs is not empty, the map key/value pairs will be set to the
// port's external_ids.
func (br *OVSBridge) CreatePort(name, ifDev string, externalIDs map[string]interface{}) (string, Error) {
	return br.createPort(name, ifDev, "", 0, externalIDs, nil)
}

func (br *OVSBridge) createPort(name, ifName, ifType string, ofPortRequest int32, externalIDs, options map[string]interface{}) (string, Error) {
	var externalIDMap []interface{}
	var optionMap []interface{}

	if externalIDs != nil {
		externalIDMap = helpers.MakeOVSDBMap(externalIDs)
	}
	if options != nil {
		optionMap = helpers.MakeOVSDBMap(options)
	}

	tx := br.ovsdb.Transaction(openvSwitchSchema)

	interf := Interface{
		Name:          ifName,
		Type:          ifType,
		OFPortRequest: ofPortRequest,
		Options:       optionMap,
	}
	ifNamedUUID := tx.Insert(dbtransaction.Insert{
		Table: "Interface",
		Row:   interf,
	})

	port := Port{
		Name: name,
		Interfaces: helpers.MakeOVSDBSet(map[string]interface{}{
			"named-uuid": []string{ifNamedUUID},
		}),
		ExternalIDs: externalIDMap,
	}
	portNamedUUID := tx.Insert(dbtransaction.Insert{
		Table: "Port",
		Row:   port,
	})

	mutateSet := helpers.MakeOVSDBSet(map[string]interface{}{
		"named-uuid": []string{portNamedUUID},
	})
	tx.Mutate(dbtransaction.Mutate{
		Table:     "Bridge",
		Mutations: [][]interface{}{{"ports", "insert", mutateSet}},
		Where:     [][]interface{}{{"name", "==", br.name}},
	})

	res, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return "", newTransactionError(err, temporary)
	}

	return res[1].UUID[1], nil
}

// Retrieves the ofport value of an interface given the interface name.
// The function will invoke OVSDB "wait" operation with 1 second timeout to wait
// the ofport is set on the interface, and so could be blocked for 1 second. If
// the "wait" operation timeout, value 0 will be returned.
func (br *OVSBridge) GetOFPort(ifName string) (int32, Error) {
	tx := br.ovsdb.Transaction(openvSwitchSchema)

	tx.Wait(dbtransaction.Wait{
		Table:   "Interface",
		Timeout: 1000,
		Columns: []string{"ofport"},
		Until:   "!=",
		Rows: []interface{}{map[string]interface{}{
			"ofport": helpers.MakeOVSDBSet(map[string]interface{}{}),
		}},
		Where: [][]interface{}{{"name", "==", ifName}},
	})
	tx.Select(dbtransaction.Select{
		Table:   "Interface",
		Columns: []string{"ofport"},
		Where:   [][]interface{}{{"name", "==", ifName}},
	})

	res, err, temporary := tx.Commit()
	if err != nil {
		// TODO: differentiate timeout error
		klog.Error("Transaction failed: ", err)
		return 0, newTransactionError(err, temporary)
	}

	ofport := int32(res[1].Rows[0].(map[string]interface{})["ofport"].(float64))
	return ofport, nil
}

func buildMapFromOVSDBMap(data []interface{}) map[string]string {
	if data[0] == "map" {
		ret := make(map[string]string)
		for _, pair := range data[1].([]interface{}) {
			ret[pair.([]interface{})[0].(string)] = pair.([]interface{})[1].(string)
		}
		return ret
	} else { // Should not be possible
		return map[string]string{}
	}
}

func buildPortDataCommon(port, intf map[string]interface{}, portData *OVSPortData) {
	portData.Name = port["name"].(string)
	portData.ExternalIDs = buildMapFromOVSDBMap(port["external_ids"].([]interface{}))
	if ofPort, ok := intf["ofport"].(float64); ok {
		portData.OFPort = int32(ofPort)
	} else { // ofport not assigned by OVS yet
		portData.OFPort = 0
	}
}

// Retrieves port data given the OVS port UUID and interface name.
// nil is returned, if the port or interface could not be found, or the
// interface is not attached to the port.
// The port's OFPort will be set to 0, if its ofport is not assigned by OVS yet.
func (br *OVSBridge) GetPortData(portUUID, ifName string) (*OVSPortData, Error) {
	tx := br.ovsdb.Transaction(openvSwitchSchema)
	tx.Select(dbtransaction.Select{
		Table:   "Port",
		Columns: []string{"name", "external_ids", "interfaces"},
		Where:   [][]interface{}{{"_uuid", "==", []string{"uuid", portUUID}}},
	})
	tx.Select(dbtransaction.Select{
		Table:   "Interface",
		Columns: []string{"_uuid", "ofport"},
		Where:   [][]interface{}{{"name", "==", ifName}},
	})

	res, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return nil, newTransactionError(err, temporary)
	}
	if len(res[0].Rows) == 0 {
		klog.Warning("Could not find port ", portUUID)
		return nil, nil
	}
	if len(res[1].Rows) == 0 {
		klog.Warning("Could not find interface ", ifName)
		return nil, newTransactionError(errors.New("Interface not exists"), false)
	}

	port := res[0].Rows[0].(map[string]interface{})
	intf := res[1].Rows[0].(map[string]interface{})
	ifUUID := intf["_uuid"].([]interface{})[1].(string)
	ifUUIDList := helpers.GetIdListFromOVSDBSet(port["interfaces"].([]interface{}))

	found := false
	for _, uuid := range ifUUIDList {
		if uuid == ifUUID {
			found = true
			break
		}
	}
	if !found {
		klog.Errorf("Interface %s is not attached to the port %s", ifName, portUUID)
		return nil, newTransactionError(errors.New("Interface is not attached to the port"), false)
	}

	portData := OVSPortData{UUID: portUUID, IFName: ifName}
	buildPortDataCommon(port, intf, &portData)
	return &portData, nil
}

// Return all ports on the bridge.
// A port's OFPort will be set to 0, if its ofport is not assigned by OVS yet.
func (br *OVSBridge) GetPortList() ([]OVSPortData, Error) {
	tx := br.ovsdb.Transaction(openvSwitchSchema)
	tx.Select(dbtransaction.Select{
		Table:   "Bridge",
		Columns: []string{"ports"},
		Where:   [][]interface{}{{"name", "==", br.name}},
	})
	tx.Select(dbtransaction.Select{
		Table:   "Port",
		Columns: []string{"_uuid", "name", "external_ids", "interfaces"},
	})
	tx.Select(dbtransaction.Select{
		Table:   "Interface",
		Columns: []string{"_uuid", "name", "ofport"},
	})

	res, err, temporary := tx.Commit()
	if err != nil {
		klog.Error("Transaction failed: ", err)
		return nil, newTransactionError(err, temporary)
	}

	if len(res[0].Rows) == 0 {
		klog.Warning("Could not find bridge")
		return []OVSPortData{}, nil
	}
	portUUIDList := helpers.GetIdListFromOVSDBSet(res[0].Rows[0].(map[string]interface{})["ports"].([]interface{}))

	portMap := make(map[string]map[string]interface{})
	for _, row := range res[1].Rows {
		uuid := row.(map[string]interface{})["_uuid"].([]interface{})[1].(string)
		portMap[uuid] = row.(map[string]interface{})
	}

	ifMap := make(map[string]map[string]interface{})
	for _, row := range res[2].Rows {
		uuid := row.(map[string]interface{})["_uuid"].([]interface{})[1].(string)
		ifMap[uuid] = row.(map[string]interface{})
	}

	portList := make([]OVSPortData, len(portUUIDList))
	for i, uuid := range portUUIDList {
		portList[i].UUID = uuid
		port := portMap[uuid]
		ifUUIDList := helpers.GetIdListFromOVSDBSet(port["interfaces"].([]interface{}))
		// Port should have one interface
		intf := ifMap[ifUUIDList[0]]
		portList[i].IFName = intf["name"].(string)
		buildPortDataCommon(port, intf, &portList[i])
	}

	return portList, nil
}