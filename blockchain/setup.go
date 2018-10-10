package blockchain

import (
	"fmt"
	"github.com/hyperledger/fabric-sdk-go/pkg/client/channel"
	"github.com/hyperledger/fabric-sdk-go/pkg/client/event"
	mspclient "github.com/hyperledger/fabric-sdk-go/pkg/client/msp"
	"github.com/hyperledger/fabric-sdk-go/pkg/client/resmgmt"
	"github.com/hyperledger/fabric-sdk-go/pkg/common/errors/retry"
	"github.com/hyperledger/fabric-sdk-go/pkg/common/providers/msp"
	"github.com/hyperledger/fabric-sdk-go/pkg/core/config"
	packager "github.com/hyperledger/fabric-sdk-go/pkg/fab/ccpackager/gopackager"
	"github.com/hyperledger/fabric-sdk-go/pkg/fabsdk"
	"github.com/hyperledger/fabric-sdk-go/third_party/github.com/hyperledger/fabric/common/cauthdsl"
    "github.com/pkg/errors"
)

type FabricSetup struct {
    ConfigFile      string
    OrgID           string
    OrdererID       string
    ChannelID       string
    ChainCodeID     string
    initialized     bool
    ChannelConfig   string
    ChainCodeGoPath string
    ChainCodePath   string
    OrgAdmin        string
    OrgName         string
    UserName        string
    client          *channel.Client
    admin           *resmgmt.Client
    sdk             *fabsdk.FabricSDK
    event           *event.Client
}

func (setup *FabricSetup) Initialize() error {
    if setup.initialized {
        return fmt.Errorf("sdk already initialized")
    }

    sdk, err := fabsdk.New(config.FromFile(setup.ConfigFile))
    if err != nil {
        return fmt.Errorf("failed to create sdk: %v", err)
    }
    setup.sdk = sdk

    // The resource management client is responsible for managing channels (create/update channel)
	resourceManagerClientContext := setup.sdk.Context(fabsdk.WithUser(setup.OrgAdmin), fabsdk.WithOrg(setup.OrgName))
	if err != nil {
		return errors.WithMessage(err, "failed to load Admin identity")
	}
	resMgmtClient, err := resmgmt.New(resourceManagerClientContext)
	if err != nil {
		return errors.WithMessage(err, "failed to create channel management client from Admin identity")
	}
	setup.admin = resMgmtClient
	fmt.Println("Ressource management client created")

	// The MSP client allow us to retrieve user information from their identity, like its signing identity which we will need to save the channel
	mspClient, err := mspclient.New(sdk.Context(), mspclient.WithOrg(setup.OrgName))
	if err != nil {
		return errors.WithMessage(err, "failed to create MSP client")
	}
	adminIdentity, err := mspClient.GetSigningIdentity(setup.OrgAdmin)
	if err != nil {
		return errors.WithMessage(err, "failed to get admin signing identity")
	}
	req := resmgmt.SaveChannelRequest{ChannelID: setup.ChannelID, ChannelConfigPath: setup.ChannelConfig, SigningIdentities: []msp.SigningIdentity{adminIdentity}}
	txID, err := setup.admin.SaveChannel(req, resmgmt.WithOrdererEndpoint(setup.OrdererID))
	if err != nil || txID.TransactionID == "" {
		return errors.WithMessage(err, "failed to save channel")
	}
    fmt.Println("Channel created")

    // Make admin user join the previously created channel
	if err = setup.admin.JoinChannel(setup.ChannelID, resmgmt.WithRetry(retry.DefaultResMgmtOpts), resmgmt.WithOrdererEndpoint(setup.OrdererID)); err != nil {
		return errors.WithMessage(err, "failed to make admin join channel")
	}
	fmt.Println("Channel joined")

	fmt.Println("Initialization Successful")
	setup.initialized = true
    return nil
}

func (setup *FabricSetup) InstallAndInstantiateCC() error {
    ccPkg, err := packager.NewCCPackage(setup.ChainCodePath, setup.ChainCodeGoPath)
    if err != nil {
        return fmt.Errorf("failed to create chaincode package: %v", err)
    }

    version := "1.31"
    installCCReq := resmgmt.InstallCCRequest{Name:setup.ChainCodeID, Path:setup.ChainCodePath, Version:version, Package:ccPkg}
    _, err = setup.admin.InstallCC(installCCReq)
    if err != nil {
        return fmt.Errorf("failed to install chaincode %s to org peers: %v", setup.ChainCodeID, err)
    }

    ccPolicy := cauthdsl.SignedByAnyMember([]string{"org1.samtest.com"})

    resp, err := setup.admin.InstantiateCC(setup.ChannelID, resmgmt.InstantiateCCRequest{Name: setup.ChainCodeID, Path: setup.ChainCodeGoPath, Version:version, Args: [][]byte{[]byte("init")}, Policy: ccPolicy})
    if err != nil || resp.TransactionID == "" {
        return fmt.Errorf("failed to instantiate the chaincode: %v", err)
    }

    // Channel client is used to query and execute transactions
	clientContext := setup.sdk.ChannelContext(setup.ChannelID, fabsdk.WithUser(setup.UserName))
	setup.client, err = channel.New(clientContext)
	if err != nil {
		return errors.WithMessage(err, "failed to create new channel client")
	}
	fmt.Println("Channel client created")

	// Creation of the client which will enables access to our channel events
	setup.event, err = event.New(clientContext)
	if err != nil {
		return errors.WithMessage(err, "failed to create new event client")
	}
	fmt.Println("Event client created")

	fmt.Println("Chaincode Installation & Instantiation Successful")
    return nil
}

func (setup *FabricSetup) CloseSDK() {
    setup.sdk.Close()
}
