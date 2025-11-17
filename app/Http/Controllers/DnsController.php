<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\DomainRepository;
use App\Repositories\DnsRecordRepository;
use App\Repositories\SiteRepository;
use App\Services\SetupDnsZoneService;
use App\Facades\Dns;
use App\Domain\Entities\DnsRecord;
use App\Support\AuditLogger;

class DnsController extends Controller
{
    public function index(Request $request): Response
    {
        $domainRepo = new DomainRepository();
        $siteRepo = new SiteRepository();
        $domains = $domainRepo->all();
        
        // Load site information for each domain
        foreach ($domains as $domain) {
            $site = $siteRepo->find($domain->siteId);
            $domain->siteDomain = $site ? $site->domain : 'Unknown';
        }
        
        return $this->view('pages/dns/index', [
            'title' => 'DNS Management',
            'domains' => $domains
        ]);
    }

    public function show(Request $request, int $id): Response
    {
        $domainRepo = new DomainRepository();
        $dnsRecordRepo = new DnsRecordRepository();
        
        $domain = $domainRepo->find($id);
        
        if (!$domain) {
            return new Response('Domain not found', 404);
        }
        
        $records = $dnsRecordRepo->findByDomainId($id);
        
        return $this->view('pages/dns/show', [
            'title' => 'DNS Records - ' . $domain->name,
            'domain' => $domain,
            'records' => $records
        ]);
    }

    public function create(Request $request): Response
    {
        $siteRepo = new SiteRepository();
        $sites = $siteRepo->all();
        
        return $this->view('pages/dns/create', [
            'title' => 'Create DNS Zone',
            'sites' => $sites
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $siteId = (int) $request->post('site_id');
            $domainName = $request->post('domain_name');
            $serverIp = $request->post('server_ip');
            
            $service = new SetupDnsZoneService(
                new DomainRepository(),
                new DnsRecordRepository(),
                new SiteRepository(),
                Dns::getInstance()
            );
            
            $domain = $service->execute(
                siteId: $siteId,
                domainName: $domainName,
                serverIp: $serverIp
            );
            
            // Log audit event
            AuditLogger::logCreated('dns_zone', $domainName, [
                'site_id' => $siteId,
                'server_ip' => $serverIp
            ]);
            
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->successAlert('DNS zone created successfully! Redirecting...'));
            }
            
            return $this->redirect('/dns');
            
        } catch (\Exception $e) {
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($e->getMessage()), 400);
            }
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function addRecord(Request $request, int $domainId): Response
    {
        try {
            $dnsRecordRepo = new DnsRecordRepository();
            
            $record = new DnsRecord(
                domainId: $domainId,
                name: $request->post('name'),
                type: $request->post('type'),
                content: $request->post('content'),
                ttl: (int) $request->post('ttl', 3600),
                priority: $request->post('priority') ? (int) $request->post('priority') : null
            );
            
            $record = $dnsRecordRepo->create($record);
            
            // Add to PowerDNS
            Dns::getInstance()->addRecord($record);
            
            // Log audit event
            AuditLogger::logCreated('dns_record', "{$record->name} ({$record->type})", [
                'domain_id' => $domainId,
                'type' => $record->type,
                'content' => $record->content
            ]);
            
            return $this->redirect("/dns/{$domainId}");
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function deleteRecord(Request $request, int $domainId, int $recordId): Response
    {
        try {
            $dnsRecordRepo = new DnsRecordRepository();
            $record = $dnsRecordRepo->find($recordId);
            
            if (!$record) {
                throw new \Exception('DNS record not found');
            }
            
            // Log audit event before deletion
            AuditLogger::logDeleted('dns_record', "{$record->name} ({$record->type})", [
                'record_id' => $recordId,
                'domain_id' => $domainId,
                'content' => $record->content
            ]);
            
            // Delete from PowerDNS
            Dns::getInstance()->deleteRecord($record);
            
            // Delete from panel database
            $dnsRecordRepo->delete($recordId);
            
            return $this->redirect("/dns/{$domainId}");
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
